// Package maxflow implements an incremental max-flow adjuster for directed
// networks with integer capacities. It supports batch capacity
// increases/decreases on existing edges. When a capacity decrease makes the
// current flow infeasible, the excess flow is withdrawn along reverse
// augmenting tracks in the residual network, then alternative augmenting
// paths are searched to restore maximality. After every adjustment a min-cut
// certificate (S/T reachability partition plus the cut edge set) is produced.
package maxflow

import (
	"fmt"
	"sort"
)

// Edge is a residual-network edge. Each user edge is represented by a forward
// Edge (Cap = capacity) paired with a reverse Edge (Cap = 0); Flow on the
// reverse edge is always the negation of the forward one.
type Edge struct {
	To   int
	Cap  int64
	Flow int64
	rev  *Edge
}

// Change describes a capacity adjustment of an existing edge.
// Delta > 0 increases capacity, Delta < 0 decreases it.
type Change struct {
	From  int
	To    int
	Delta int64
}

// CutEdge is one saturated edge crossing the min cut from the S side to the
// T side.
type CutEdge struct {
	From int
	To   int
	Cap  int64
	Flow int64
}

// Certificate is the S-T cut certificate produced after each adjustment:
// the new max-flow value, the node reachability partition, and the cut edges.
type Certificate struct {
	Value    int64
	SSide    []int
	TSide    []int
	CutEdges []CutEdge
}

// Network is a directed flow network with integer capacities.
type Network struct {
	n     int
	s     int
	t     int
	g     [][]*Edge
	fwd   map[[2]int]*Edge // user edges keyed by (from, to)
	order [][2]int         // insertion order of user edges, for stable output
	level []int
	iter  []int
}

// NewNetwork creates a network with n nodes numbered 0..n-1, source s, sink t.
func NewNetwork(n, s, t int) *Network {
	return &Network{
		n:   n,
		s:   s,
		t:   t,
		g:   make([][]*Edge, n),
		fwd: make(map[[2]int]*Edge),
	}
}

// AddEdge adds a directed edge u -> v with the given capacity.
func (n *Network) AddEdge(u, v int, cap int64) error {
	if u < 0 || u >= n.n || v < 0 || v >= n.n {
		return fmt.Errorf("maxflow: node out of range: %d -> %d", u, v)
	}
	if u == v {
		return fmt.Errorf("maxflow: self loop %d -> %d not allowed", u, v)
	}
	if cap < 0 {
		return fmt.Errorf("maxflow: negative capacity %d on %d -> %d", cap, u, v)
	}
	key := [2]int{u, v}
	if _, ok := n.fwd[key]; ok {
		return fmt.Errorf("maxflow: duplicate edge %d -> %d", u, v)
	}
	f := &Edge{To: v, Cap: cap}
	b := &Edge{To: u, Cap: 0}
	f.rev, b.rev = b, f
	n.g[u] = append(n.g[u], f)
	n.g[v] = append(n.g[v], b)
	n.fwd[key] = f
	n.order = append(n.order, key)
	return nil
}

// Flow returns the current flow on the user edge u -> v.
func (n *Network) Flow(u, v int) (int64, error) {
	e, ok := n.fwd[[2]int{u, v}]
	if !ok {
		return 0, fmt.Errorf("maxflow: no edge %d -> %d", u, v)
	}
	return e.Flow, nil
}

// Capacity returns the current capacity of the user edge u -> v.
func (n *Network) Capacity(u, v int) (int64, error) {
	e, ok := n.fwd[[2]int{u, v}]
	if !ok {
		return 0, fmt.Errorf("maxflow: no edge %d -> %d", u, v)
	}
	return e.Cap, nil
}

// Value returns the current s-t flow value (net flow out of the source).
func (n *Network) Value() int64 {
	var v int64
	for _, e := range n.g[n.s] {
		v += e.Flow
	}
	return v
}

// MaxFlow augments the current flow to a maximum flow (Dinic's algorithm,
// incremental: it continues from the flow already present) and returns the
// maximum flow value.
func (n *Network) MaxFlow() int64 {
	for n.bfsLevels() {
		n.iter = make([]int, n.n)
		for n.dfs(n.s, n.t, maxInt64) > 0 {
		}
	}
	return n.Value()
}

// Adjust applies a batch of capacity changes to existing edges, repairs
// feasibility by withdrawing excess flow along reverse residual tracks,
// re-augments to a new maximum flow, and returns the min-cut certificate.
func (n *Network) Adjust(changes []Change) (Certificate, error) {
	// Validate the whole batch before mutating anything.
	for _, c := range changes {
		e, ok := n.fwd[[2]int{c.From, c.To}]
		if !ok {
			return Certificate{}, fmt.Errorf("maxflow: no edge %d -> %d", c.From, c.To)
		}
		if e.Cap+c.Delta < 0 {
			return Certificate{}, fmt.Errorf("maxflow: capacity of %d -> %d would become negative (%d %+d)",
				c.From, c.To, e.Cap, c.Delta)
		}
	}
	// Apply new capacities.
	for _, c := range changes {
		n.fwd[[2]int{c.From, c.To}].Cap += c.Delta
	}
	// Withdraw flow that now exceeds capacity, along reverse residual tracks.
	for _, c := range changes {
		key := [2]int{c.From, c.To}
		e := n.fwd[key]
		if e.Flow > e.Cap {
			if err := n.withdraw(c.From, c.To, e, e.Flow-e.Cap); err != nil {
				return Certificate{}, err
			}
		}
	}
	// Re-augment along alternative paths to restore maximality.
	n.MaxFlow()
	return n.Cert(), nil
}

// Cert computes the min-cut certificate for the current flow: nodes reachable
// from s in the residual network form the S side, the rest the T side, and
// the saturated user edges crossing S -> T form the cut set.
func (n *Network) Cert() Certificate {
	reach := n.reachableFromS()
	cert := Certificate{Value: n.Value()}
	for i := 0; i < n.n; i++ {
		if reach[i] {
			cert.SSide = append(cert.SSide, i)
		} else {
			cert.TSide = append(cert.TSide, i)
		}
	}
	for _, key := range n.order {
		e := n.fwd[key]
		if reach[key[0]] && !reach[key[1]] {
			cert.CutEdges = append(cert.CutEdges, CutEdge{
				From: key[0], To: key[1], Cap: e.Cap, Flow: e.Flow,
			})
		}
	}
	sort.Slice(cert.CutEdges, func(i, j int) bool {
		if cert.CutEdges[i].From != cert.CutEdges[j].From {
			return cert.CutEdges[i].From < cert.CutEdges[j].From
		}
		return cert.CutEdges[i].To < cert.CutEdges[j].To
	})
	return cert
}

// withdraw removes delta units of flow passing through edge e = (u, v):
// it pushes delta back from u to s and from t to v through the residual
// network (cancelling flow on an s~>u path and a v~>t path), then cancels
// delta on e itself. Each augmentation is a reverse track in the residual
// network, so the s-t flow value drops by exactly delta.
func (n *Network) withdraw(u, v int, e *Edge, delta int64) error {
	if err := n.augment(u, n.s, delta); err != nil {
		return fmt.Errorf("maxflow: withdraw %d -> %d: %v", u, v, err)
	}
	if err := n.augment(n.t, v, delta); err != nil {
		return fmt.Errorf("maxflow: withdraw %d -> %d: %v", u, v, err)
	}
	e.Flow -= delta
	e.rev.Flow += delta
	return nil
}

// augment pushes delta units along residual paths from `from` to `to`,
// iterating path-by-path up to each path's bottleneck capacity.
func (n *Network) augment(from, to int, delta int64) error {
	for delta > 0 {
		prev := make([]*Edge, n.n)
		visited := make([]bool, n.n)
		visited[from] = true
		queue := []int{from}
		for len(queue) > 0 && !visited[to] {
			x := queue[0]
			queue = queue[1:]
			for _, e := range n.g[x] {
				if !visited[e.To] && e.Cap-e.Flow > 0 {
					visited[e.To] = true
					prev[e.To] = e
					queue = append(queue, e.To)
				}
			}
		}
		if !visited[to] {
			return fmt.Errorf("no residual path %d -> %d", from, to)
		}
		bottleneck := delta
		for x := to; x != from; {
			if e := prev[x]; e.Cap-e.Flow < bottleneck {
				bottleneck = e.Cap - e.Flow
			}
			x = prev[x].rev.To
		}
		for x := to; x != from; {
			e := prev[x]
			e.Flow += bottleneck
			e.rev.Flow -= bottleneck
			x = e.rev.To
		}
		delta -= bottleneck
	}
	return nil
}

// reachableFromS returns the set of nodes reachable from s via edges with
// positive residual capacity.
func (n *Network) reachableFromS() []bool {
	reach := make([]bool, n.n)
	reach[n.s] = true
	queue := []int{n.s}
	for len(queue) > 0 {
		x := queue[0]
		queue = queue[1:]
		for _, e := range n.g[x] {
			if !reach[e.To] && e.Cap-e.Flow > 0 {
				reach[e.To] = true
				queue = append(queue, e.To)
			}
		}
	}
	return reach
}

// bfsLevels builds the level graph on residual capacities; returns false when
// t is unreachable from s.
func (n *Network) bfsLevels() bool {
	n.level = make([]int, n.n)
	for i := range n.level {
		n.level[i] = -1
	}
	n.level[n.s] = 0
	queue := []int{n.s}
	for len(queue) > 0 {
		x := queue[0]
		queue = queue[1:]
		for _, e := range n.g[x] {
			if n.level[e.To] < 0 && e.Cap-e.Flow > 0 {
				n.level[e.To] = n.level[x] + 1
				queue = append(queue, e.To)
			}
		}
	}
	return n.level[n.t] >= 0
}

// dfs pushes flow along level-graph edges; returns the amount pushed.
func (n *Network) dfs(u, t int, f int64) int64 {
	if u == t {
		return f
	}
	for ; n.iter[u] < len(n.g[u]); n.iter[u]++ {
		e := n.g[u][n.iter[u]]
		if e.Cap-e.Flow > 0 && n.level[e.To] == n.level[u]+1 {
			if d := n.dfs(e.To, t, min64(f, e.Cap-e.Flow)); d > 0 {
				e.Flow += d
				e.rev.Flow -= d
				return d
			}
		}
	}
	return 0
}

const maxInt64 = int64(^uint64(0) >> 1)

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
