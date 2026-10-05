package hgsolve

import (
	"container/heap"
	"math"
)

type nodeItem struct {
	n int
	d float64
}

type nodeHeap []*nodeItem

func (h nodeHeap) Len() int           { return len(h) }
func (h nodeHeap) Less(i, j int) bool { return h[i].d < h[j].d }
func (h nodeHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *nodeHeap) Push(x any)        { *h = append(*h, x.(*nodeItem)) }
func (h *nodeHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

// TreeCost is the per-node shortest proof cost when sharing is ignored:
// dist(to) = sum(dist(premises)) + cost. Because a shared subproof is
// charged once per use, this OVERESTIMATES the true DAG cost and therefore
// serves as an upper bound on the optimum. This is exactly the "simple
// per-point shortest path" that is not exact for this problem.
func (g *Graph) TreeCost(facts []string, target string) (float64, bool) {
	const inf = math.MaxFloat64
	dist := make([]float64, len(g.names))
	for i := range dist {
		dist[i] = inf
	}
	rem := make([]int, len(g.Edges))
	sum := make([]float64, len(g.Edges))
	for i := range g.Edges {
		rem[i] = len(g.prem[i])
	}
	pq := &nodeHeap{}
	push := func(n int, d float64) {
		if d < dist[n] {
			dist[n] = d
			heap.Push(pq, &nodeItem{n: n, d: d})
		}
	}
	for _, f := range facts {
		if i, ok := g.index[f]; ok {
			push(i, 0)
		}
	}
	for i := range g.Edges {
		if rem[i] == 0 {
			push(g.tgt[i], g.Edges[i].Cost)
		}
	}
	for pq.Len() > 0 {
		it := heap.Pop(pq).(*nodeItem)
		if it.d > dist[it.n] {
			continue // stale
		}
		for _, ei := range g.byPrem[it.n] {
			rem[ei]--
			sum[ei] += it.d
			if rem[ei] == 0 {
				push(g.tgt[ei], sum[ei]+g.Edges[ei].Cost)
			}
		}
	}
	ti, ok := g.index[target]
	if !ok || dist[ti] == inf {
		return 0, false
	}
	return dist[ti], true
}

// OrLower relaxes every hyperedge to "any single premise suffices", turning
// the problem into an ordinary shortest-path computation. Every real proof
// contains a fact-to-target path of distinct edges whose total cost is at
// most the proof's DAG cost, so this UNDERESTIMATES the optimum and serves
// as an admissible lower bound.
func (g *Graph) OrLower(facts []string, target string) (float64, bool) {
	const inf = math.MaxFloat64
	dist := make([]float64, len(g.names))
	for i := range dist {
		dist[i] = inf
	}
	pq := &nodeHeap{}
	push := func(n int, d float64) {
		if d < dist[n] {
			dist[n] = d
			heap.Push(pq, &nodeItem{n: n, d: d})
		}
	}
	for _, f := range facts {
		if i, ok := g.index[f]; ok {
			push(i, 0)
		}
	}
	for i := range g.Edges {
		if len(g.prem[i]) == 0 {
			push(g.tgt[i], g.Edges[i].Cost)
		}
	}
	for pq.Len() > 0 {
		it := heap.Pop(pq).(*nodeItem)
		if it.d > dist[it.n] {
			continue
		}
		for _, ei := range g.byPrem[it.n] {
			push(g.tgt[ei], it.d+g.Edges[ei].Cost)
		}
	}
	ti, ok := g.index[target]
	if !ok || dist[ti] == inf {
		return 0, false
	}
	return dist[ti], true
}

// GreedyProof repeatedly fires the cheapest fireable edge until target is
// derived, then prunes edges that do not support the target. The result is a
// valid proof, hence an upper bound on the optimum; it is also used to prune
// the exact search.
func (g *Graph) GreedyProof(facts []string, target string) (cost float64, edgeIDs []string, ok bool) {
	ti, tok := g.index[target]
	inSet := make([]bool, len(g.names))
	for _, f := range facts {
		if i, has := g.index[f]; has {
			inSet[i] = true
		}
	}
	if tok && inSet[ti] {
		return 0, nil, true
	}
	used := make([]bool, len(g.Edges))
	for {
		best := -1
		for ei := range g.Edges {
			if used[ei] || inSet[g.tgt[ei]] {
				continue
			}
			fireable := true
			for _, p := range g.prem[ei] {
				if !inSet[p] {
					fireable = false
					break
				}
			}
			if !fireable {
				continue
			}
			if best == -1 || g.Edges[ei].Cost < g.Edges[best].Cost {
				best = ei
			}
		}
		if best == -1 {
			return 0, nil, false
		}
		used[best] = true
		inSet[g.tgt[best]] = true
		if tok && inSet[ti] {
			return g.pruneProof(facts, target, used)
		}
	}
}

// pruneProof keeps only the fired edges that actually support target and
// recomputes the cost over that subset. Facts need no supporting edge.
func (g *Graph) pruneProof(facts []string, target string, used []bool) (float64, []string, bool) {
	isFact := make([]bool, len(g.names))
	for _, f := range facts {
		if i, ok := g.index[f]; ok {
			isFact[i] = true
		}
	}
	// An edge supports the proof iff its target is a needed node.
	neededNode := make([]bool, len(g.names))
	neededNode[g.index[target]] = true
	keep := make([]bool, len(g.Edges))
	// Fixpoint: small graphs, simple loop.
	for changed := true; changed; {
		changed = false
		for ei := range g.Edges {
			if !used[ei] || keep[ei] || !neededNode[g.tgt[ei]] {
				continue
			}
			keep[ei] = true
			changed = true
			for _, p := range g.prem[ei] {
				if !isFact[p] && !neededNode[p] {
					neededNode[p] = true
				}
			}
		}
	}
	cost := 0.0
	var ids []string
	for ei := range g.Edges {
		if keep[ei] {
			cost += g.Edges[ei].Cost
			ids = append(ids, g.Edges[ei].ID)
		}
	}
	return cost, ids, true
}
