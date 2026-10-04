package main

import (
	"container/heap"
	"math"
)

// maxExactNodes caps the subset-search state space at 2^maxExactNodes.
const maxExactNodes = 62

// NodeResult is the per-node outcome.
type NodeResult struct {
	Node      string
	Derivable bool
	Exact     bool    // true if Cost is the proven optimum
	Cost      float64 // exact min proof cost (only meaningful if Exact)
	Lower     float64 // admissible lower bound
	Upper     float64 // tree-expansion upper bound (sharing ignored)
	Proof     []int   // indices into SolveResult.Edges, in firing order
}

// SolveResult holds the full solver output.
type SolveResult struct {
	Edges     []Hyperedge // augmented edges (facts first), indexed by Proof
	Nodes     map[string]*NodeResult
	Order     []string // sorted node names
	Truncated bool     // exact search hit the state budget
	Explored  int      // states popped by the exact search
}

type parentInfo struct {
	prev uint64
	edge int
}

type stateItem struct {
	mask uint64
	cost float64
}

type statePQ []stateItem

func (q statePQ) Len() int            { return len(q) }
func (q statePQ) Less(i, j int) bool  { return q[i].cost < q[j].cost }
func (q statePQ) Swap(i, j int)       { q[i], q[j] = q[j], q[i] }
func (q *statePQ) Push(x interface{}) { *q = append(*q, x.(stateItem)) }
func (q *statePQ) Pop() interface{} {
	old := *q
	n := len(old)
	it := old[n-1]
	*q = old[:n-1]
	return it
}

// Solve runs the bounded exact search plus the bound computations.
//
// Exactness argument: a state is the set of already-proven nodes; firing an
// edge adds its target and charges its cost once. States only grow and costs
// are non-negative, so Dijkstra pops states in non-decreasing cost order and
// the first popped state containing node v has cost equal to the minimum
// cost of any proof DAG for v (shared subproofs charged once, since each
// edge enters the state path at most once). If the state budget is
// exhausted, nodes already recorded remain exact; the rest only carry
// [Lower, Upper] bounds.
func (p *Problem) Solve(maxStates int) *SolveResult {
	aug := p.augmented()
	edges, names := liveEdges(aug)

	res := &SolveResult{
		Edges: edges,
		Nodes: map[string]*NodeResult{},
		Order: names,
	}

	lower, upper := nodeBounds(edges)
	derivable := closure(edges)
	for _, n := range names {
		nr := &NodeResult{Node: n, Derivable: derivable[n]}
		nr.Lower = math.Inf(1)
		nr.Upper = math.Inf(1)
		if v, ok := lower[n]; ok {
			nr.Lower = v
		}
		if v, ok := upper[n]; ok {
			nr.Upper = v
		}
		res.Nodes[n] = nr
	}

	if len(names) > maxExactNodes {
		res.Truncated = true
		return res
	}
	if maxStates <= 0 {
		maxStates = 1 << 30
	}

	index := map[string]int{}
	for i, n := range names {
		index[n] = i
	}
	premMask := make([]uint64, len(edges))
	targetBit := make([]uint64, len(edges))
	for i, e := range edges {
		for _, pre := range e.Premises {
			premMask[i] |= 1 << uint(index[pre])
		}
		targetBit[i] = 1 << uint(index[e.Target])
	}

	dist := map[uint64]float64{0: 0}
	parent := map[uint64]parentInfo{}
	bestMask := map[string]uint64{}
	recorded := map[string]bool{}
	want := 0
	for _, n := range names {
		if derivable[n] {
			want++
		}
	}

	pq := &statePQ{{mask: 0, cost: 0}}
	heap.Init(pq)
	for pq.Len() > 0 {
		it := heap.Pop(pq).(stateItem)
		if it.cost > dist[it.mask]+1e-12 {
			continue
		}
		res.Explored++
		if res.Explored > maxStates {
			res.Truncated = true
			break
		}
		for i, n := range names {
			if it.mask&(1<<uint(i)) != 0 && !recorded[n] {
				recorded[n] = true
				bestMask[n] = it.mask
				nr := res.Nodes[n]
				nr.Exact = true
				nr.Cost = it.cost
			}
		}
		if len(recorded) == want {
			break
		}
		for i := range edges {
			if premMask[i]&it.mask != premMask[i] {
				continue
			}
			if it.mask&targetBit[i] != 0 {
				continue // target already proven; never useful
			}
			nm := it.mask | targetBit[i]
			nc := it.cost + edges[i].Cost
			if d, ok := dist[nm]; !ok || nc < d-1e-12 {
				dist[nm] = nc
				parent[nm] = parentInfo{prev: it.mask, edge: i}
				heap.Push(pq, stateItem{mask: nm, cost: nc})
			}
		}
	}

	for _, n := range names {
		nr := res.Nodes[n]
		if !nr.Exact {
			continue
		}
		nr.Proof = proofEdges(parent, bestMask[n])
	}
	return res
}

// proofEdges walks the parent chain back to the empty state and returns the
// edge indices in firing order. Each edge appears at most once, so the
// returned set is the minimal proof DAG (shared nodes included once).
func proofEdges(parent map[uint64]parentInfo, mask uint64) []int {
	var rev []int
	for mask != 0 {
		pi := parent[mask]
		rev = append(rev, pi.edge)
		mask = pi.prev
	}
	out := make([]int, len(rev))
	for i, e := range rev {
		out[len(rev)-1-i] = e
	}
	return out
}
