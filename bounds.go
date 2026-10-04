package main

import "container/heap"

type candItem struct {
	node string
	cost float64
}

type candPQ []candItem

func (q candPQ) Len() int            { return len(q) }
func (q candPQ) Less(i, j int) bool  { return q[i].cost < q[j].cost }
func (q candPQ) Swap(i, j int)       { q[i], q[j] = q[j], q[i] }
func (q *candPQ) Push(x interface{}) { *q = append(*q, x.(candItem)) }
func (q *candPQ) Pop() interface{} {
	old := *q
	n := len(old)
	it := old[n-1]
	*q = old[:n-1]
	return it
}

// nodeBounds computes, for every node, an admissible lower bound and a
// valid upper bound on the minimum shared-proof cost.
//
// Both are least fixpoints of hypergraph recurrences, solved exactly with a
// generalized Dijkstra (edge candidate = edge cost + accumulate(finalized
// premise values); accumulate dominates each operand, so label setting is
// sound):
//
//	Lower(v) = min over edges e->v of cost(e) + max over premises p of Lower(p)
//	Upper(v) = min over edges e->v of cost(e) + sum over premises p of Upper(p)
//
// Lower is admissible because any proof of v through e contains a full
// subproof of every premise, so its cost is at least cost(e) plus the
// largest individual premise minimum. Upper counts shared subtrees once per
// use (tree expansion); the min-cost DAG proof is itself such a tree after
// duplication, so Upper never undershoots the true shared optimum.
func nodeBounds(edges []Hyperedge) (lower, upper map[string]float64) {
	lower = hyperFixpoint(edges, func(acc, x float64) float64 {
		if x > acc {
			return x
		}
		return acc
	})
	upper = hyperFixpoint(edges, func(acc, x float64) float64 {
		return acc + x
	})
	return lower, upper
}

func hyperFixpoint(edges []Hyperedge, accum func(acc, x float64) float64) map[string]float64 {
	// users[p] lists edges that have p as a premise.
	users := map[string][]int{}
	missing := make([]int, len(edges))
	acc := make([]float64, len(edges))
	for i, e := range edges {
		seen := map[string]bool{}
		for _, pre := range e.Premises {
			if seen[pre] {
				continue
			}
			seen[pre] = true
			users[pre] = append(users[pre], i)
			missing[i]++
		}
	}

	dist := map[string]float64{}
	pq := &candPQ{}
	heap.Init(pq)
	for i, e := range edges {
		if missing[i] == 0 {
			heap.Push(pq, candItem{node: e.Target, cost: e.Cost})
		}
	}
	for pq.Len() > 0 {
		it := heap.Pop(pq).(candItem)
		if _, done := dist[it.node]; done {
			continue
		}
		dist[it.node] = it.cost
		for _, ei := range users[it.node] {
			missing[ei]--
			acc[ei] = accum(acc[ei], it.cost)
			if missing[ei] == 0 {
				heap.Push(pq, candItem{
					node: edges[ei].Target,
					cost: edges[ei].Cost + acc[ei],
				})
			}
		}
	}
	return dist
}
