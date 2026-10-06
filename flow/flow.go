// Package flow solves exact integer min-cost flow problems on small
// transportation networks (up to ~8 nodes / ~12 edges), supporting
// negative arc costs, phased flow commitments (executed amounts that
// cannot be reduced) and cancelled arcs.
//
// The exact solver is successive shortest paths (Bellman-Ford) followed
// by residual negative-cycle cancelling, so the result is a provably
// optimal feasible flow: a feasible flow is min-cost iff its residual
// graph contains no negative-cost cycle.
package flow

import (
	"errors"
	"fmt"
)

const INF = int64(1) << 60

// Edge is a directed capacitated arc with a linear (possibly negative)
// per-unit cost.
type Edge struct {
	From int
	To   int
	Cap  int64 // total capacity
	Cost int64 // per-unit cost, may be negative

	// Lower is the already-executed (committed) flow on this arc.
	// Any new plan must carry at least Lower units on it.
	Lower int64

	// Cancelled arcs accept no new flow, but the executed amount
	// (Lower) still stands: the arc flow is frozen at Lower.
	Cancelled bool
}

// Problem is a min-cost flow instance with node balance requirements.
// Net[v] > 0 means v must send Net[v] more units than it receives.
// Net must sum to zero.
type Problem struct {
	NumNodes int
	Net      []int64
	Edges    []Edge
}

// NewTransportation builds a Problem from a source/sink/demand triple.
func NewTransportation(numNodes, source, sink int, demand int64, edges []Edge) *Problem {
	net := make([]int64, numNodes)
	net[source] = demand
	net[sink] = -demand
	return &Problem{NumNodes: numNodes, Net: net, Edges: edges}
}

// CutEvidence certifies infeasibility: the nodes reachable from the
// super source in the final residual graph (S side) cannot push the
// Deficit remaining units across the saturated cut into the T side.
type CutEvidence struct {
	SNodes     []int `json:"s_nodes"`
	TNodes     []int `json:"t_nodes"`
	Deficit    int64 `json:"deficit"`
	CrossEdges []int `json:"cross_edges"` // original edge indices saturated across the cut
	UnmetNodes []int `json:"unmet_nodes"` // nodes whose required inflow could not be met
}

// Solution is the outcome of an exact solve.
type Solution struct {
	Feasible bool         `json:"feasible"`
	Flows    []int64      `json:"flows"` // per-edge total flow, executed amount included
	Cost     int64        `json:"cost"`
	Cut      *CutEvidence `json:"cut,omitempty"`
}

// effective bounds for an edge after applying commitments/cancellation.
func bounds(e Edge) (lo, hi int64, err error) {
	lo, hi = e.Lower, e.Cap
	if e.Cancelled {
		hi = lo
	}
	if lo < 0 || hi < 0 || lo > hi {
		return 0, 0, fmt.Errorf("edge %d->%d: invalid bounds [lower=%d, cap=%d]", e.From, e.To, lo, hi)
	}
	return lo, hi, nil
}

// Bounds exposes the effective [lo, hi] interval of every edge.
func Bounds(p *Problem) ([]int64, []int64, error) {
	lo := make([]int64, len(p.Edges))
	hi := make([]int64, len(p.Edges))
	for i, e := range p.Edges {
		l, h, err := bounds(e)
		if err != nil {
			return nil, nil, fmt.Errorf("edge %d: %v", i, err)
		}
		lo[i], hi[i] = l, h
	}
	return lo, hi, nil
}

// ---- residual graph ----

type rEdge struct {
	to   int
	rev  int
	cap  int64
	cost int64
}

type graph struct {
	adj [][]rEdge
}

func newGraph(n int) *graph { return &graph{adj: make([][]rEdge, n)} }

// addEdge appends a forward/backward residual pair and returns the
// index of the forward edge in adj[from]. Self-loops (from == to) are
// handled by fixing the reverse indices explicitly.
func (g *graph) addEdge(from, to int, cap, cost int64) int {
	idx := len(g.adj[from])
	fwd := rEdge{to: to, rev: len(g.adj[to]), cap: cap, cost: cost}
	if from == to {
		fwd.rev = idx + 1
	}
	bwd := rEdge{to: from, rev: idx, cap: 0, cost: -cost}
	g.adj[from] = append(g.adj[from], fwd)
	g.adj[to] = append(g.adj[to], bwd)
	return idx
}

func (g *graph) addFlow(u, ei int, f int64) {
	e := &g.adj[u][ei]
	e.cap -= f
	g.adj[e.to][e.rev].cap += f
}

// bellmanFord computes shortest distances from s in the residual graph.
func (g *graph) bellmanFord(s int) (dist []int64, parNode, parEdge []int) {
	n := len(g.adj)
	dist = make([]int64, n)
	parNode = make([]int, n)
	parEdge = make([]int, n)
	for i := range dist {
		dist[i] = INF
		parNode[i] = -1
	}
	dist[s] = 0
	for i := 0; i < n-1; i++ {
		updated := false
		for u := 0; u < n; u++ {
			if dist[u] >= INF {
				continue
			}
			for ei, e := range g.adj[u] {
				if e.cap > 0 && dist[u]+e.cost < dist[e.to] {
					dist[e.to] = dist[u] + e.cost
					parNode[e.to] = u
					parEdge[e.to] = ei
					updated = true
				}
			}
		}
		if !updated {
			break
		}
	}
	return dist, parNode, parEdge
}

// findNegativeCycle returns one negative-cost residual cycle as a list
// of (node, edgeIndex) pairs, or nil if none exists. Distances start at
// zero for every node so cycles disconnected from any source are found.
func (g *graph) findNegativeCycle() [][2]int {
	n := len(g.adj)
	dist := make([]int64, n)
	parNode := make([]int, n)
	parEdge := make([]int, n)
	for i := range parNode {
		parNode[i] = -1
	}
	x := -1
	for i := 0; i < n; i++ {
		x = -1
		for u := 0; u < n; u++ {
			for ei, e := range g.adj[u] {
				if e.cap > 0 && dist[u]+e.cost < dist[e.to] {
					dist[e.to] = dist[u] + e.cost
					parNode[e.to] = u
					parEdge[e.to] = ei
					x = e.to
				}
			}
		}
	}
	if x == -1 {
		return nil
	}
	// Walk parent pointers; the first repeated node delimits a genuine
	// simple directed cycle (any tail before the cycle entry is dropped).
	for i := 0; i < n && parNode[x] >= 0; i++ {
		x = parNode[x]
	}
	order := []int{}
	pos := map[int]int{}
	for v := x; ; v = parNode[v] {
		if p, ok := pos[v]; ok {
			cyc := make([][2]int, 0, len(order)-p)
			var cost int64
			for _, u := range order[p:] {
				pe := [2]int{parNode[u], parEdge[u]}
				cost += g.adj[pe[0]][pe[1]].cost
				cyc = append(cyc, pe)
			}
			if cost >= 0 {
				return nil // defensive: parent cycle must be negative
			}
			return cyc
		}
		if parNode[v] < 0 {
			return nil // unreachable once a round-n relaxation exists
		}
		pos[v] = len(order)
		order = append(order, v)
	}
}

// Solve finds an exact min-cost feasible flow honouring all executed
// commitments and cancellations. If the required balances cannot be
// met it reports infeasibility together with a min-cut witness.
func Solve(p *Problem) (*Solution, error) {
	if p.NumNodes <= 0 {
		return nil, errors.New("NumNodes must be positive")
	}
	if len(p.Net) != p.NumNodes {
		return nil, errors.New("len(Net) != NumNodes")
	}
	var sum int64
	for _, b := range p.Net {
		sum += b
	}
	if sum != 0 {
		return nil, errors.New("node balances must sum to zero")
	}
	lo, hi, err := Bounds(p)
	if err != nil {
		return nil, err
	}

	// Node balances after pre-sending the committed (lower) amounts.
	need := make([]int64, p.NumNodes)
	copy(need, p.Net)
	for i, e := range p.Edges {
		need[e.From] -= lo[i]
		need[e.To] += lo[i]
	}

	n := p.NumNodes
	ss, tt := n, n+1
	g := newGraph(n + 2)
	fwdIdx := make([]int, len(p.Edges))
	for i, e := range p.Edges {
		fwdIdx[i] = g.addEdge(e.From, e.To, hi[i]-lo[i], e.Cost)
	}
	var total int64
	for v := 0; v < n; v++ {
		switch {
		case need[v] > 0: // supply: super source feeds it
			g.addEdge(ss, v, need[v], 0)
			total += need[v]
		case need[v] < 0: // demand: drains into the super sink
			g.addEdge(v, tt, -need[v], 0)
		}
	}

	// Exact optimization loop. Negative residual cycles are cancelled
	// first; only when none exist is a shortest augmenting path used
	// (Bellman-Ford parents are then guaranteed acyclic). Terminates
	// with a min-cost flow of value `total`, or earlier if the super
	// sink becomes unreachable, proving infeasibility.
	var sent int64
	for {
		if cyc := g.findNegativeCycle(); cyc != nil {
			aug := INF
			for _, pe := range cyc {
				if c := g.adj[pe[0]][pe[1]].cap; c < aug {
					aug = c
				}
			}
			for _, pe := range cyc {
				g.addFlow(pe[0], pe[1], aug)
			}
			continue
		}
		if sent >= total {
			break
		}
		dist, parNode, parEdge := g.bellmanFord(ss)
		if dist[tt] >= INF {
			break
		}
		aug := total - sent
		for v := tt; v != ss; v = parNode[v] {
			if c := g.adj[parNode[v]][parEdge[v]].cap; c < aug {
				aug = c
			}
		}
		for v := tt; v != ss; v = parNode[v] {
			g.addFlow(parNode[v], parEdge[v], aug)
		}
		sent += aug
	}

	if sent < total {
		return &Solution{Feasible: false, Cut: cutEvidence(g, p, ss, tt, total-sent)}, nil
	}

	sol := &Solution{Feasible: true, Flows: make([]int64, len(p.Edges))}
	for i, e := range p.Edges {
		residual := g.adj[e.From][fwdIdx[i]].cap
		sol.Flows[i] = hi[i] - residual
		sol.Cost += sol.Flows[i] * e.Cost
	}
	return sol, nil
}

// cutEvidence extracts the min-cut witness from the residual graph.
func cutEvidence(g *graph, p *Problem, ss, tt int, deficit int64) *CutEvidence {
	n := len(g.adj)
	reach := make([]bool, n)
	queue := []int{ss}
	reach[ss] = true
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		for _, e := range g.adj[u] {
			if e.cap > 0 && !reach[e.to] {
				reach[e.to] = true
				queue = append(queue, e.to)
			}
		}
	}
	cut := &CutEvidence{Deficit: deficit}
	for v := 0; v < p.NumNodes; v++ {
		if reach[v] {
			cut.SNodes = append(cut.SNodes, v)
		} else {
			cut.TNodes = append(cut.TNodes, v)
		}
	}
	inS := make([]bool, p.NumNodes)
	for _, v := range cut.SNodes {
		inS[v] = true
	}
	for i, e := range p.Edges {
		if inS[e.From] && !inS[e.To] {
			cut.CrossEdges = append(cut.CrossEdges, i)
		}
	}
	// Demand nodes whose arc into the super sink is not saturated
	// could not receive their full required inflow.
	for v := 0; v < p.NumNodes; v++ {
		for _, e := range g.adj[v] {
			if e.to == tt && e.cap > 0 {
				cut.UnmetNodes = append(cut.UnmetNodes, v)
			}
		}
	}
	return cut
}
