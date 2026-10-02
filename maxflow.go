// Package maxflow implements an incremental max-flow adjuster for directed
// networks with integer capacities. It supports batch capacity increases and
// decreases on existing edges. When a capacity reduction makes the current
// flow infeasible, the excess flow is withdrawn along the residual network
// (reverse retraction), alternative augmenting paths are then searched, and a
// min-cut certificate (S/T reachability partition plus cut edge set) is
// produced for every update.
package maxflow

import "fmt"

// edge is a residual-graph edge. Forward (user) edges carry a non-negative
// id; their paired reverse edges carry id == -1 and their residual capacity
// equals the flow currently routed on the forward edge.
type edge struct {
	to  int
	rev int // index of the paired edge in g[to]
	cap int64
	id  int
}

// edgeLoc points to the forward residual edge for a user edge id.
type edgeLoc struct {
	from int
	idx  int
}

// CapUpdate requests setting the capacity of edge ID to NewCap.
type CapUpdate struct {
	ID     int
	NewCap int64
}

// CutEdge is a user edge crossing the min cut from the S side to the T side.
type CutEdge struct {
	ID       int
	From, To int
	Cap      int64
}

// UpdateResult reports the outcome of one (batch) capacity update.
type UpdateResult struct {
	MaxFlow     int64     // max-flow value after the update
	Retracted   int64     // flow withdrawn along the residual network
	ReAugmented int64     // flow recovered via new augmenting paths
	Reachable   []bool    // Reachable[v] == true iff v is on the S side
	Cut         []CutEdge // edges crossing from the S side to the T side
}

// Network is a directed flow network with integer capacities.
type Network struct {
	g         [][]edge
	loc       []edgeLoc // per user edge id: location of its forward edge
	caps      []int64   // per user edge id: current capacity
	s, t      int
	flowValue int64
	hasST     bool
	// lastPushed records the bottleneck of the most recent augment call,
	// so maximize can reuse augment without recomputing the amount.
	lastPushed int64
}

// NewNetwork returns an empty network with n nodes numbered 0..n-1.
func NewNetwork(n int) *Network {
	return &Network{g: make([][]edge, n)}
}

// AddEdge adds a directed edge and returns its id for later updates.
func (n *Network) AddEdge(from, to int, cap int64) int {
	if cap < 0 {
		panic("maxflow: negative capacity")
	}
	id := len(n.loc)
	fwd := edge{to: to, rev: len(n.g[to]), cap: cap, id: id}
	rev := edge{to: from, rev: len(n.g[from]), cap: 0, id: -1}
	n.g[from] = append(n.g[from], fwd)
	n.g[to] = append(n.g[to], rev)
	n.loc = append(n.loc, edgeLoc{from: from, idx: len(n.g[from]) - 1})
	n.caps = append(n.caps, cap)
	return id
}

// Flow returns the current flow on the user edge with the given id.
func (n *Network) Flow(id int) int64 {
	l := n.loc[id]
	return n.g[n.g[l.from][l.idx].to][n.g[l.from][l.idx].rev].cap
}

// Value returns the current s-t flow value.
func (n *Network) Value() int64 { return n.flowValue }

// MaxFlow sets source and sink and computes the initial max flow,
// returning its value.
func (n *Network) MaxFlow(s, t int) int64 {
	n.s, n.t = s, t
	n.hasST = true
	n.maximize()
	return n.flowValue
}

// UpdateCapacity applies a single capacity change and returns the result
// certificate.
func (n *Network) UpdateCapacity(id int, newCap int64) UpdateResult {
	return n.BatchUpdate([]CapUpdate{{ID: id, NewCap: newCap}})
}

// BatchUpdate applies a batch of capacity changes on existing edges.
// Reductions that violate the current flow trigger reverse retraction along
// the residual network; afterwards the network is re-augmented to the new
// maximum and a min-cut certificate is computed.
func (n *Network) BatchUpdate(updates []CapUpdate) UpdateResult {
	if !n.hasST {
		panic("maxflow: call MaxFlow(s, t) before updating capacities")
	}
	before := n.flowValue
	for _, u := range updates {
		n.applyUpdate(u)
	}
	retracted := before - n.flowValue
	n.maximize()
	reAugmented := n.flowValue - (before - retracted)
	reach := n.reachableFromSource()
	return UpdateResult{
		MaxFlow:     n.flowValue,
		Retracted:   retracted,
		ReAugmented: reAugmented,
		Reachable:   reach,
		Cut:         n.cutEdges(reach),
	}
}

// applyUpdate sets a new capacity on one edge, retracting excess flow if the
// edge's current flow exceeds the new capacity.
func (n *Network) applyUpdate(u CapUpdate) {
	if u.ID < 0 || u.ID >= len(n.loc) {
		panic(fmt.Sprintf("maxflow: unknown edge id %d", u.ID))
	}
	if u.NewCap < 0 {
		panic("maxflow: negative capacity")
	}
	l := n.loc[u.ID]
	fwd := &n.g[l.from][l.idx]
	rev := &n.g[fwd.to][fwd.rev]
	flow := rev.cap
	delta := u.NewCap - n.caps[u.ID]
	fwd.cap += delta
	n.caps[u.ID] = u.NewCap
	if flow <= u.NewCap {
		return
	}
	// The edge is over capacity: clamp its flow and repair conservation.
	excess := flow - u.NewCap
	rev.cap = u.NewCap
	fwd.cap = 0
	n.retract(l.from, fwd.to, excess)
}

// retract restores flow conservation after the flow on edge (u, v) was
// reduced by excess: the surplus at u is pushed back to the source s along
// residual (reverse) paths, and the deficit at v is pulled back from the
// sink t. Each pushed unit lowers the s-t flow value by one.
func (n *Network) retract(u, v int, excess int64) {
	pushed := n.augment(u, n.s, excess)
	if pushed < excess {
		n.augment(n.t, v, excess-pushed)
	}
	n.flowValue -= excess
}

// maximize augments the current flow to a maximum s-t flow (Edmonds-Karp).
func (n *Network) maximize() {
	for {
		if n.augment(n.s, n.t, 0) == 0 {
			return
		}
		n.flowValue += n.lastPushed
	}
}

// augment finds one shortest residual path from src to dst by BFS and pushes
// flow along it, up to limit units (limit <= 0 means unlimited). It returns
// the amount pushed (0 when no path exists).
func (n *Network) augment(src, dst int, limit int64) int64 {
	type parent struct {
		from int
		idx  int
	}
	prev := make([]parent, len(n.g))
	seen := make([]bool, len(n.g))
	seen[src] = true
	queue := []int{src}
	for len(queue) > 0 && !seen[dst] {
		v := queue[0]
		queue = queue[1:]
		for i, e := range n.g[v] {
			if e.cap > 0 && !seen[e.to] {
				seen[e.to] = true
				prev[e.to] = parent{from: v, idx: i}
				if e.to == dst {
					break
				}
				queue = append(queue, e.to)
			}
		}
	}
	if !seen[dst] {
		n.lastPushed = 0
		return 0
	}
	// Bottleneck of the discovered path.
	bottleneck := int64(-1)
	for v := dst; v != src; {
		p := prev[v]
		if c := n.g[p.from][p.idx].cap; bottleneck < 0 || c < bottleneck {
			bottleneck = c
		}
		v = p.from
	}
	if limit > 0 && bottleneck > limit {
		bottleneck = limit
	}
	for v := dst; v != src; {
		p := prev[v]
		n.g[p.from][p.idx].cap -= bottleneck
		rev := n.g[p.from][p.idx].rev
		n.g[v][rev].cap += bottleneck
		v = p.from
	}
	n.lastPushed = bottleneck
	return bottleneck
}

// reachableFromSource returns the S side of the min cut: nodes reachable
// from s in the residual network of the current max flow.
func (n *Network) reachableFromSource() []bool {
	reach := make([]bool, len(n.g))
	reach[n.s] = true
	queue := []int{n.s}
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		for _, e := range n.g[v] {
			if e.cap > 0 && !reach[e.to] {
				reach[e.to] = true
				queue = append(queue, e.to)
			}
		}
	}
	return reach
}

// cutEdges lists the user edges crossing from the S side to the T side.
// Their capacities sum to the max-flow value (the certificate).
func (n *Network) cutEdges(reach []bool) []CutEdge {
	var cut []CutEdge
	for from := range n.g {
		if !reach[from] {
			continue
		}
		for _, e := range n.g[from] {
			if e.id >= 0 && !reach[e.to] {
				cut = append(cut, CutEdge{
					ID:   e.id,
					From: from,
					To:   e.to,
					Cap:  n.caps[e.id],
				})
			}
		}
	}
	return cut
}
