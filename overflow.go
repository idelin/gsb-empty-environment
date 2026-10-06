// Package overflow simulates liquid flowing through a network of
// fixed-capacity containers connected by a directed acyclic overflow graph.
//
// On every tick the simulator first pours the requested external inflow into
// each container, then propagates the over-capacity part along the graph in
// topological order until the network is stable. Overflow is split across
// outgoing edges by exact rational fractions; any fraction of the overflow
// that is not assigned to an edge (i.e. 1 - sum(fractions), or all of it
// when the container is a sink) is recorded as discharge.
//
// All arithmetic uses math/big.Rat, so total liquid is conserved exactly:
//
//	sum(levels before) + sum(adds) == sum(levels after) + discharged
package overflow

import (
	"fmt"
	"math/big"
	"sort"
)

// Container is a single tank with a fixed capacity and a current level.
type Container struct {
	ID       string
	Capacity *big.Rat
	Level    *big.Rat
}

// Edge carries a rational Fraction of the source container's overflow to
// the target container. Fraction must be in (0, 1], and the fractions of
// all edges leaving one container must sum to at most 1.
type Edge struct {
	From     string
	To       string
	Fraction *big.Rat
}

// Network is a compiled, ready-to-run simulation.
type Network struct {
	containers map[string]*Container
	order      []*Container // topological order of the overflow graph
	outgoing   map[string][]Edge

	// Discharged accumulates every drop that left the network across all
	// ticks. It is the exact sink term of the conservation invariant.
	Discharged *big.Rat
}

// Builder incrementally constructs a Network.
type Builder struct {
	containers map[string]*big.Rat
	edges      []Edge
}

// NewBuilder returns an empty Builder.
func NewBuilder() *Builder {
	return &Builder{containers: make(map[string]*big.Rat)}
}

// AddContainer registers a container with the given capacity. The level
// starts at zero.
func (b *Builder) AddContainer(id string, capacity *big.Rat) error {
	if capacity == nil || capacity.Sign() < 0 {
		return fmt.Errorf("overflow: container %q has nil or negative capacity", id)
	}
	if _, dup := b.containers[id]; dup {
		return fmt.Errorf("overflow: duplicate container %q", id)
	}
	b.containers[id] = new(big.Rat).Set(capacity)
	return nil
}

// AddEdge registers a directed overflow edge. Both endpoints must exist.
func (b *Builder) AddEdge(from, to string, fraction *big.Rat) error {
	if fraction == nil || fraction.Sign() <= 0 || fraction.Cmp(big.NewRat(1, 1)) > 0 {
		return fmt.Errorf("overflow: edge %q->%q fraction must be in (0, 1]", from, to)
	}
	if _, ok := b.containers[from]; !ok {
		return fmt.Errorf("overflow: unknown container %q", from)
	}
	if _, ok := b.containers[to]; !ok {
		return fmt.Errorf("overflow: unknown container %q", to)
	}
	if from == to {
		return fmt.Errorf("overflow: self-loop on %q", from)
	}
	b.edges = append(b.edges, Edge{From: from, To: to, Fraction: new(big.Rat).Set(fraction)})
	return nil
}

// Build validates the graph (per-node fraction sums, acyclicity) and
// returns a Network whose containers are processed in topological order.
func (b *Builder) Build() (*Network, error) {
	n := &Network{
		containers: make(map[string]*Container, len(b.containers)),
		outgoing:   make(map[string][]Edge),
		Discharged: new(big.Rat),
	}
	for id, cap := range b.containers {
		n.containers[id] = &Container{ID: id, Capacity: new(big.Rat).Set(cap), Level: new(big.Rat)}
	}
	sums := make(map[string]*big.Rat)
	indegree := make(map[string]int, len(b.containers))
	for id := range b.containers {
		indegree[id] = 0
	}
	for _, e := range b.edges {
		n.outgoing[e.From] = append(n.outgoing[e.From], e)
		s := sums[e.From]
		if s == nil {
			s = new(big.Rat)
		}
		s.Add(s, e.Fraction)
		sums[e.From] = s
		if s.Cmp(big.NewRat(1, 1)) > 0 {
			return nil, fmt.Errorf("overflow: fractions leaving %q sum to %s > 1", e.From, s.RatString())
		}
		indegree[e.To]++
	}

	// Kahn's algorithm; deterministic order via sorted queue.
	var queue []string
	for id, d := range indegree {
		if d == 0 {
			queue = append(queue, id)
		}
	}
	sort.Strings(queue)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		n.order = append(n.order, n.containers[id])
		var next []string
		for _, e := range n.outgoing[id] {
			indegree[e.To]--
			if indegree[e.To] == 0 {
				next = append(next, e.To)
			}
		}
		sort.Strings(next)
		queue = append(queue, next...)
	}
	if len(n.order) != len(n.containers) {
		return nil, fmt.Errorf("overflow: graph contains a cycle")
	}
	return n, nil
}

// Container returns the container with the given id, or nil.
func (n *Network) Container(id string) *Container { return n.containers[id] }

// TotalLevel returns the exact sum of all container levels.
func (n *Network) TotalLevel() *big.Rat {
	t := new(big.Rat)
	for _, c := range n.containers {
		t.Add(t, c.Level)
	}
	return t
}

// Tick performs one simulation step: it pours adds into the containers and
// then propagates overflow along the topological order of the graph until
// every container is within capacity. Because the graph is acyclic, a
// single ordered pass reaches the stable state. It returns the exact amount
// discharged during this tick.
func (n *Network) Tick(adds map[string]*big.Rat) (*big.Rat, error) {
	for id, amt := range adds {
		c, ok := n.containers[id]
		if !ok {
			return nil, fmt.Errorf("overflow: unknown container %q", id)
		}
		if amt == nil || amt.Sign() < 0 {
			return nil, fmt.Errorf("overflow: negative or nil inflow for %q", id)
		}
		c.Level.Add(c.Level, amt)
	}

	discharged := new(big.Rat)
	for _, c := range n.order {
		overflow := new(big.Rat).Sub(c.Level, c.Capacity)
		if overflow.Sign() <= 0 {
			continue
		}
		c.Level.Set(c.Capacity)
		assigned := new(big.Rat)
		for _, e := range n.outgoing[c.ID] {
			flow := new(big.Rat).Mul(overflow, e.Fraction)
			n.containers[e.To].Level.Add(n.containers[e.To].Level, flow)
			assigned.Add(assigned, flow)
		}
		// The unassigned remainder leaves the network as discharge.
		discharged.Add(discharged, new(big.Rat).Sub(overflow, assigned))
	}
	n.Discharged.Add(n.Discharged, discharged)
	return discharged, nil
}
