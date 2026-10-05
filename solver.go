package main

import (
	"sort"
	"time"
)

// Solution is a feasible feedback vertex set with its objective values.
type Solution struct {
	Deleted []bool
	Cost    int64
	Count   int
}

// Output is the JSON result of a solve run.
type Output struct {
	Status     string   `json:"status"` // "optimal" or "interrupted"
	Deleted    []string `json:"deleted"`
	Cost       int64    `json:"cost"`
	Count      int      `json:"count"`
	TopoOrder  []string `json:"topological_order"`
	LowerBound int64    `json:"lower_bound"`
	UpperBound int64    `json:"upper_bound"`
	Nodes      int64    `json:"nodes_explored"`
}

// solutionLess orders solutions lexicographically by (cost, count, sorted IDs):
// cheapest first, then fewest vertices, then smallest ID sequence.
func solutionLess(a, b *Solution, g *Graph) bool {
	if a.Cost != b.Cost {
		return a.Cost < b.Cost
	}
	if a.Count != b.Count {
		return a.Count < b.Count
	}
	ai, bi := sortedIDs(g, a.Deleted), sortedIDs(g, b.Deleted)
	for i := 0; i < len(ai) && i < len(bi); i++ {
		if ai[i] != bi[i] {
			return ai[i] < bi[i]
		}
	}
	return len(ai) < len(bi)
}

// workItem is one open branch-and-bound node: the vertices deleted so far.
// lbCost is a valid lower bound on the total cost of any solution in this
// subtree; it is refined when the node is expanded.
type workItem struct {
	deleted []bool
	cost    int64
	count   int
	lbCost  int64
}

const deadlineCheckInterval = 64

// Solve finds the optimal feedback vertex set by branch and bound. The
// objective is lexicographic: minimum deletion cost, then minimum number of
// deleted vertices, then smallest sorted ID sequence. When budget > 0 the
// search stops after the budget elapses and reports the incumbent (upper
// bound) together with the best open-frontier lower bound.
func Solve(g *Graph, budget time.Duration) *Output {
	return solve(g, budget, 0)
}

// solve additionally accepts a node budget (maxNodes > 0), which gives tests
// a deterministic way to exercise the interruption path.
func solve(g *Graph, budget time.Duration, maxNodes int64) *Output {
	n := len(g.IDs)
	limited := budget > 0
	var deadline time.Time
	if limited {
		deadline = time.Now().Add(budget)
	}

	// Incumbent: a greedy solution (always feasible, never worse than
	// deleting everything), so the upper bound is meaningful even when
	// the search is interrupted before reaching the first leaf.
	best := g.greedySolution()

	interrupted := false
	var nodes int64
	stack := []*workItem{{deleted: make([]bool, n)}}

	for len(stack) > 0 {
		if maxNodes > 0 && nodes >= maxNodes {
			interrupted = true
			break
		}
		if limited && nodes%deadlineCheckInterval == 0 && time.Now().After(deadline) {
			interrupted = true
			break
		}
		nodes++
		item := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		alive := make([]bool, n)
		for i := range alive {
			alive[i] = !item.deleted[i]
		}
		deleted := item.deleted
		cost, count := item.cost, item.count

		// Forced deletions: a self-loop is a cycle of length one, so its
		// vertex belongs to every feedback vertex set.
		cloned := false
		for v := 0; v < n; v++ {
			if alive[v] && g.Self[v] {
				if !cloned {
					deleted = append([]bool(nil), item.deleted...)
					cloned = true
				}
				deleted[v] = true
				alive[v] = false
				cost += g.Cost[v]
				count++
			}
		}

		// Reduction: a vertex lies on a directed cycle iff it belongs to a
		// non-trivial SCC (self-loops were handled above). Vertices outside
		// every cycle are kept for free and never deleted by an optimum.
		active := make([]bool, n)
		anyActive := false
		for _, comp := range g.SCCs(alive) {
			if len(comp) > 1 {
				for _, v := range comp {
					active[v] = true
				}
				anyActive = true
			}
		}

		if !anyActive {
			sol := &Solution{Deleted: deleted, Cost: cost, Count: count}
			if solutionLess(sol, best, g) {
				best = sol
			}
			continue
		}

		// Lower bound from a vertex-disjoint cycle packing.
		lbAddCost, lbAddCount := g.cyclePackingBound(active)
		item.lbCost = cost + lbAddCost
		lbCount := count + lbAddCount
		// Prune only when the subtree is strictly worse in (cost, count);
		// ties must be explored because the ID order is still undecided.
		if item.lbCost > best.Cost || (item.lbCost == best.Cost && lbCount > best.Count) {
			continue
		}

		// Branch on a shortest cycle: every feedback vertex set deletes at
		// least one of its vertices. Note that one deletion per SCC is not
		// assumed to suffice; the recursion re-examines the remaining SCCs.
		cyc := g.shortestCycle(active)
		sort.Slice(cyc, func(i, j int) bool {
			if g.Cost[cyc[i]] != g.Cost[cyc[j]] {
				return g.Cost[cyc[i]] < g.Cost[cyc[j]]
			}
			return g.IDs[cyc[i]] < g.IDs[cyc[j]]
		})
		for i := len(cyc) - 1; i >= 0; i-- {
			v := cyc[i]
			childDeleted := append([]bool(nil), deleted...)
			childDeleted[v] = true
			stack = append(stack, &workItem{
				deleted: childDeleted,
				cost:    cost + g.Cost[v],
				count:   count + 1,
				lbCost:  item.lbCost, // parent bound stays valid for children
			})
		}
	}

	lower := best.Cost
	if interrupted {
		// The optimum is bounded below by the cheapest open frontier node.
		lower = stack[0].lbCost
		for _, it := range stack[1:] {
			if it.lbCost < lower {
				lower = it.lbCost
			}
		}
	}
	topo, _ := g.TopoOrder(best.Deleted)
	status := "optimal"
	if interrupted {
		status = "interrupted"
	}
	return &Output{
		Status:     status,
		Deleted:    sortedIDs(g, best.Deleted),
		Cost:       best.Cost,
		Count:      best.Count,
		TopoOrder:  topo,
		LowerBound: lower,
		UpperBound: best.Cost,
		Nodes:      nodes,
	}
}

// greedySolution deletes, until acyclic, the cheapest vertex of whatever
// cycle remains (self-loops are forced). Ties break toward smaller IDs.
func (g *Graph) greedySolution() *Solution {
	n := len(g.IDs)
	deleted := make([]bool, n)
	alive := make([]bool, n)
	for i := range alive {
		alive[i] = true
	}
	var cost int64
	count := 0
	remove := func(v int) {
		deleted[v] = true
		alive[v] = false
		cost += g.Cost[v]
		count++
	}
	for v := 0; v < n; v++ {
		if g.Self[v] {
			remove(v)
		}
	}
	for {
		active := make([]bool, n)
		anyActive := false
		for _, comp := range g.SCCs(alive) {
			if len(comp) > 1 {
				for _, v := range comp {
					active[v] = true
				}
				anyActive = true
			}
		}
		if !anyActive {
			return &Solution{Deleted: deleted, Cost: cost, Count: count}
		}
		cyc := g.anyCycle(active)
		pick := cyc[0]
		for _, v := range cyc[1:] {
			if g.Cost[v] < g.Cost[pick] ||
				(g.Cost[v] == g.Cost[pick] && g.IDs[v] < g.IDs[pick]) {
				pick = v
			}
		}
		remove(pick)
	}
}
