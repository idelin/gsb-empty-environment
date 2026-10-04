package mincostflow

import "math/big"

// BruteResult holds the outcome of an exhaustive enumeration of all
// flows. It is intended for cross-checking Solve on small instances.
type BruteResult struct {
	Feasible bool
	Cost     *big.Int // minimum cost over all feasible flows
	Flow     []int64  // a flow attaining Cost
	Count    int      // number of feasible flows found
}

// BruteForce enumerates every combination of per-edge flows in
// [Lower, Upper], keeps those satisfying all node balances, and
// returns the minimum cost among them. It is exponential and only
// meant for small verification instances.
func BruteForce(g *Graph) *BruteResult {
	res := &BruteResult{}
	m := len(g.Edges)
	f := make([]int64, m)
	best := make([]int64, m)
	// net[v] accumulates inflow-outflow as edges are fixed.
	net := make([]int64, g.NumNodes)

	var rec func(e int)
	rec = func(e int) {
		if e == m {
			for v := 0; v < g.NumNodes; v++ {
				if -net[v] != g.Balance[v] {
					return
				}
			}
			res.Count++
			cost := FlowCost(g, f)
			if !res.Feasible || cost.Cmp(res.Cost) < 0 {
				res.Feasible = true
				res.Cost = cost
				copy(best, f)
			}
			return
		}
		ed := &g.Edges[e]
		for val := ed.Lower; val <= ed.Upper; val++ {
			f[e] = val
			net[ed.From] -= val
			net[ed.To] += val
			rec(e + 1)
			net[ed.From] += val
			net[ed.To] -= val
		}
	}
	rec(0)
	if res.Feasible {
		res.Flow = best
	}
	return res
}
