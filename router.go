package dvsim

import "sort"

// Infinity is the metric cap: any metric >= Infinity means "unreachable".
// Because metrics are clamped at Infinity, count-to-infinity is bounded and
// can never run forever.
const Infinity = 16

// UpdateEntry is one (destination, metric) pair inside a routing update.
type UpdateEntry struct {
	Dest   string
	Metric int
}

// Update is a full-table distance-vector advertisement. Seq is a per-sender
// monotonically increasing version number; receivers drop updates whose Seq
// is not newer than the last one accepted from that sender, which makes
// reordered (async) delivery safe.
type Update struct {
	From    string
	Seq     uint64
	Entries []UpdateEntry
}

type route struct {
	nextHop   string
	metric    int
	refreshed int
}

type neighbor struct {
	cost int
	up   bool
}

// Router is a distance-vector router using split horizon with poisoned
// reverse, triggered updates and versioned messages.
type Router struct {
	id  string
	net *Network

	table     map[string]*route
	neighbors map[string]*neighbor
	lastSeq   map[string]uint64
	seq       uint64

	pendingTrig bool
	maxMetric   int
	lastChange  int
}

func newRouter(id string, net *Network) *Router {
	r := &Router{
		id:        id,
		net:       net,
		table:     make(map[string]*route),
		neighbors: make(map[string]*neighbor),
		lastSeq:   make(map[string]uint64),
	}
	r.table[id] = &route{nextHop: id, metric: 0, refreshed: 0}
	return r
}

// ID returns the router identifier.
func (r *Router) ID() string { return r.id }

// Metric returns the current metric to dest and whether a route exists.
func (r *Router) Metric(dest string) (int, bool) {
	rt, ok := r.table[dest]
	if !ok {
		return Infinity, false
	}
	return rt.metric, true
}

// NextHop returns the next hop toward dest.
func (r *Router) NextHop(dest string) (string, bool) {
	rt, ok := r.table[dest]
	if !ok {
		return "", false
	}
	return rt.nextHop, true
}

// MaxMetricSeen returns the largest finite metric ever installed.
func (r *Router) MaxMetricSeen() int { return r.maxMetric }

// LastChangeTick returns the tick of the last routing-table change.
func (r *Router) LastChangeTick() int { return r.lastChange }

// Table returns a sorted snapshot of (dest, nextHop, metric).
func (r *Router) Table() []UpdateEntry {
	out := make([]UpdateEntry, 0, len(r.table))
	for dest := range r.table {
		out = append(out, UpdateEntry{Dest: dest, Metric: r.table[dest].metric})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Dest < out[j].Dest })
	return out
}

func (r *Router) install(dest, nextHop string, metric int) {
	if metric >= Infinity {
		delete(r.table, dest)
		r.changed()
		return
	}
	rt, ok := r.table[dest]
	if !ok {
		r.table[dest] = &route{nextHop: nextHop, metric: metric, refreshed: r.net.sim.Now()}
		if metric > r.maxMetric {
			r.maxMetric = metric
		}
		r.changed()
		return
	}
	if rt.nextHop == nextHop && rt.metric == metric {
		rt.refreshed = r.net.sim.Now()
		return
	}
	rt.nextHop = nextHop
	rt.metric = metric
	rt.refreshed = r.net.sim.Now()
	if metric > r.maxMetric {
		r.maxMetric = metric
	}
	r.changed()
}

func (r *Router) changed() {
	r.lastChange = r.net.sim.Now()
	r.triggerUpdate()
}

// Receive processes an incoming update. Stale or reordered updates are
// dropped by the version rule; updates from a down link are ignored.
func (r *Router) Receive(u Update) {
	if u.Seq <= r.lastSeq[u.From] {
		return // stale / reordered duplicate
	}
	r.lastSeq[u.From] = u.Seq
	nb, ok := r.neighbors[u.From]
	if !ok || !nb.up {
		return
	}
	seen := make(map[string]bool, len(u.Entries))
	for _, e := range u.Entries {
		seen[e.Dest] = true
		if e.Dest == r.id {
			continue
		}
		metric := e.Metric + nb.cost
		if metric > Infinity {
			metric = Infinity
		}
		rt, ok := r.table[e.Dest]
		switch {
		case !ok:
			if metric < Infinity {
				r.install(e.Dest, u.From, metric)
			}
		case rt.nextHop == u.From:
			// Same next hop: always accept the neighbor's latest view,
			// even if it got worse (this is what kills count-to-infinity
			// once the cap is reached and the entry disappears).
			if metric >= Infinity {
				delete(r.table, e.Dest)
				r.changed()
			} else {
				r.install(e.Dest, u.From, metric)
			}
		case metric < rt.metric:
			r.install(e.Dest, u.From, metric)
		}
	}
	// Full-table semantics: a destination previously learned via this
	// neighbor that is no longer advertised is gone.
	for dest, rt := range r.table {
		if dest == r.id {
			continue
		}
		if rt.nextHop == u.From && !seen[dest] {
			delete(r.table, dest)
			r.changed()
		}
	}
}

// buildUpdate builds the advertisement for one neighbor, applying split
// horizon with poisoned reverse: routes learned via that neighbor are
// announced back with metric Infinity.
func (r *Router) buildUpdate(nid string) Update {
	entries := make([]UpdateEntry, 0, len(r.table))
	for dest, rt := range r.table {
		metric := rt.metric
		if rt.nextHop == nid && dest != nid {
			metric = Infinity
		}
		entries = append(entries, UpdateEntry{Dest: dest, Metric: metric})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Dest < entries[j].Dest })
	r.seq++
	return Update{From: r.id, Seq: r.seq, Entries: entries}
}

// advertise sends a (poison-reverse) full-table update to every up neighbor.
func (r *Router) advertise() {
	for nid, nb := range r.neighbors {
		if nb.up {
			r.net.send(r.id, nid, r.buildUpdate(nid))
		}
	}
}

// triggerUpdate schedules a coalesced triggered update one tick later.
func (r *Router) triggerUpdate() {
	if r.pendingTrig {
		return
	}
	r.pendingTrig = true
	r.net.sim.Schedule(1, func() {
		r.pendingTrig = false
		r.advertise()
	})
}

// sweep expires routes that stopped being refreshed (silent neighbor) and
// reschedules itself. Together with the metric cap this guarantees no
// permanent count-to-infinity even if messages are lost.
func (r *Router) sweep() {
	now := r.net.sim.Now()
	for dest, rt := range r.table {
		if dest == r.id {
			continue
		}
		if now-rt.refreshed > RouteTimeout {
			delete(r.table, dest)
			r.changed()
		}
	}
}

func (r *Router) run() {
	r.advertise()
	r.sweep()
	r.net.sim.Schedule(UpdatePeriod, r.run)
}

// linkUp is called by the network when a link to nid comes up.
func (r *Router) linkUp(nid string, cost int) {
	r.neighbors[nid] = &neighbor{cost: cost, up: true}
	r.lastSeq[nid] = 0 // fresh peer epoch: accept its new versions
	r.install(nid, nid, cost)
	r.net.send(r.id, nid, r.buildUpdate(nid))
	r.advertise()
}

// linkDown is called by the network when the link to nid fails.
func (r *Router) linkDown(nid string) {
	if nb, ok := r.neighbors[nid]; ok {
		nb.up = false
	}
	for dest, rt := range r.table {
		if dest != r.id && rt.nextHop == nid {
			delete(r.table, dest)
			r.changed()
		}
	}
}
