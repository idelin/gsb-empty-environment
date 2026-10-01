package dvsim

import (
	"fmt"
	"math/rand"
	"sort"
)

// UpdatePeriod is the number of ticks between periodic full-table updates.
const UpdatePeriod = 10

// RouteTimeout is how long an unrefreshed route survives, in ticks.
const RouteTimeout = 35

// Link is a bidirectional edge with a cost; it can be up or down.
type Link struct {
	A, B string
	Cost int
	Up   bool
}

// Network owns the routers, links and the simulated message queues.
// Messages are delivered after a random bounded delay, so updates sent in
// one order may arrive in another (async, reordering channel).
type Network struct {
	sim      *Simulator
	routers  map[string]*Router
	links    map[string]*Link
	rng      *rand.Rand
	maxDelay int

	Delivered int
	Dropped   int
}

// NewNetwork creates a network on top of sim. seed makes the random message
// delays reproducible.
func NewNetwork(sim *Simulator, seed int64) *Network {
	return &Network{
		sim:      sim,
		routers:  make(map[string]*Router),
		links:    make(map[string]*Link),
		rng:      rand.New(rand.NewSource(seed)),
		maxDelay: 5,
	}
}

func linkKey(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + "|" + b
}

// AddRouter adds a router and starts its periodic update loop.
func (n *Network) AddRouter(id string) *Router {
	if _, ok := n.routers[id]; ok {
		panic("duplicate router " + id)
	}
	r := newRouter(id, n)
	n.routers[id] = r
	n.sim.Schedule(0, r.run)
	return r
}

// Router returns the router with the given id.
func (n *Network) Router(id string) *Router { return n.routers[id] }

// AddLink adds an up link between a and b with the given cost.
func (n *Network) AddLink(a, b string, cost int) {
	if cost <= 0 {
		panic("link cost must be positive")
	}
	k := linkKey(a, b)
	if _, ok := n.links[k]; ok {
		panic("duplicate link " + k)
	}
	n.links[k] = &Link{A: a, B: b, Cost: cost, Up: true}
	n.routers[a].linkUp(b, cost)
	n.routers[b].linkUp(a, cost)
}

// SetLink brings a link up or down at the current tick.
func (n *Network) SetLink(a, b string, up bool) {
	l, ok := n.links[linkKey(a, b)]
	if !ok {
		panic("no such link")
	}
	if l.Up == up {
		return
	}
	l.Up = up
	if up {
		n.routers[a].linkUp(b, l.Cost)
		n.routers[b].linkUp(a, l.Cost)
	} else {
		n.routers[a].linkDown(b)
		n.routers[b].linkDown(a)
	}
}

// send queues an update for delivery over the link from->to with a random
// delay in [1, maxDelay]. Delivery is a scheduled event, not real traffic.
func (n *Network) send(from, to string, u Update) {
	l, ok := n.links[linkKey(from, to)]
	if !ok || !l.Up {
		n.Dropped++
		return
	}
	delay := 1 + n.rng.Intn(n.maxDelay)
	n.sim.Schedule(delay, func() {
		if !l.Up {
			n.Dropped++
			return
		}
		n.Delivered++
		n.routers[to].Receive(u)
	})
}

// RouterIDs returns sorted router ids.
func (n *Network) RouterIDs() []string {
	ids := make([]string, 0, len(n.routers))
	for id := range n.routers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Links returns the links in deterministic order.
func (n *Network) Links() []*Link {
	out := make([]*Link, 0, len(n.links))
	for _, l := range n.links {
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].A != out[j].A {
			return out[i].A < out[j].A
		}
		return out[i].B < out[j].B
	})
	return out
}

// ConvergedTick returns the latest tick at which any router changed its
// table, i.e. the network has been stable since then.
func (n *Network) ConvergedTick() int {
	max := 0
	for _, r := range n.routers {
		if r.lastChange > max {
			max = r.lastChange
		}
	}
	return max
}

// DumpTables returns a human-readable rendering of all routing tables.
func (n *Network) DumpTables() string {
	s := ""
	for _, id := range n.RouterIDs() {
		r := n.routers[id]
		s += fmt.Sprintf("router %s:\n", id)
		for _, e := range r.Table() {
			next, _ := r.NextHop(e.Dest)
			s += fmt.Sprintf("  dest %s  metric %2d  via %s\n", e.Dest, e.Metric, next)
		}
	}
	return s
}
