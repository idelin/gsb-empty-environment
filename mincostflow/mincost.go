package mincostflow

import "math/big"

// resEdge is one arc of the residual network built from the current
// flow on the original edges.
type resEdge struct {
	to   int
	cap  int64
	cost *big.Int
	orig int  // index of the original edge
	dir  int8 // +1: forward residual, -1: backward residual
}

// buildResidual constructs the residual network of g with respect to
// the feasible flow f: every original edge contributes a forward arc
// with capacity Upper-f and cost +c, and a backward arc with capacity
// f-Lower and cost -c.
func buildResidual(g *Graph, f []int64) [][]resEdge {
	res := make([][]resEdge, g.NumNodes)
	for i, e := range g.Edges {
		if fwd := e.Upper - f[i]; fwd > 0 {
			res[e.From] = append(res[e.From], resEdge{to: e.To, cap: fwd, cost: e.Cost, orig: i, dir: 1})
		}
		if bwd := f[i] - e.Lower; bwd > 0 {
			neg := new(big.Int).Neg(e.Cost)
			res[e.To] = append(res[e.To], resEdge{to: e.From, cap: bwd, cost: neg, orig: i, dir: -1})
		}
	}
	return res
}

// findNegativeCycle looks for a negative-cost cycle in the residual
// network using Bellman-Ford from a virtual super source (all
// distances start at 0). It returns the cycle as a list of residual
// arcs in traversal order, or nil if the current flow is optimal.
func findNegativeCycle(res [][]resEdge) []resEdge {
	n := len(res)
	dist := make([]*big.Int, n)
	for i := range dist {
		dist[i] = new(big.Int)
	}
	type parentInfo struct {
		from int
		edge resEdge
	}
	parent := make([]parentInfo, n)

	x := -1
	for iter := 0; iter < n; iter++ {
		x = -1
		for u := 0; u < n; u++ {
			for _, e := range res[u] {
				cand := new(big.Int).Add(dist[u], e.cost)
				if cand.Cmp(dist[e.to]) < 0 {
					dist[e.to] = cand
					parent[e.to] = parentInfo{from: u, edge: e}
					if iter == n-1 {
						x = e.to
					}
				}
			}
		}
	}
	if x == -1 {
		return nil
	}

	// Walk n parents to land inside the cycle, then collect it.
	y := x
	for i := 0; i < n; i++ {
		y = parent[y].from
	}
	cycle := []resEdge{parent[y].edge}
	for cur := parent[y].from; cur != y; cur = parent[cur].from {
		cycle = append(cycle, parent[cur].edge)
	}
	// Reverse so arcs follow each other head-to-tail.
	for i, j := 0, len(cycle)-1; i < j; i, j = i+1, j-1 {
		cycle[i], cycle[j] = cycle[j], cycle[i]
	}
	return cycle
}

// cancelNegativeCycles repeatedly finds a negative-cost residual cycle
// and augments the flow around it by the bottleneck capacity, until no
// negative cycle remains. With integer costs and finite capacities the
// total cost is bounded below and strictly decreases each round, so
// the loop terminates with a minimum-cost feasible flow.
func cancelNegativeCycles(g *Graph, f []int64) {
	for {
		res := buildResidual(g, f)
		cycle := findNegativeCycle(res)
		if cycle == nil {
			return
		}
		bottleneck := cycle[0].cap
		for _, e := range cycle {
			bottleneck = min64(bottleneck, e.cap)
		}
		for _, e := range cycle {
			f[e.orig] += int64(e.dir) * bottleneck
		}
	}
}
