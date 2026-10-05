package rcsp

import (
	"container/heap"
	"fmt"
)

// expState is a (node, risk-used) vertex of the expanded state graph.
type expState struct {
	node int
	risk int
}

// expDist is the best known objective tuple for an expanded state.
type expDist struct {
	cost int
	hops int
	seq  []int
}

// betterExp reports whether a is strictly better than b under the
// objective ordering (cost, hops, edge-ID sequence).
func betterExp(a, b *expDist) bool {
	if a.cost != b.cost {
		return a.cost < b.cost
	}
	if a.hops != b.hops {
		return a.hops < b.hops
	}
	return compareSeq(a.seq, b.seq) < 0
}

type expItem struct {
	st expState
	d  *expDist
}

type expHeap []expItem

func (h expHeap) Len() int { return len(h) }
func (h expHeap) Less(i, j int) bool {
	a, b := h[i].d, h[j].d
	if a.cost != b.cost {
		return a.cost < b.cost
	}
	if a.hops != b.hops {
		return a.hops < b.hops
	}
	return compareSeq(a.seq, b.seq) < 0
}
func (h expHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *expHeap) Push(x interface{}) {
	*h = append(*h, x.(expItem))
}
func (h *expHeap) Pop() interface{} {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

// SolveByStateExpansion is an independent verifier meant for small
// graphs. It expands the graph into an explicit state graph of
// (node, risk-used) pairs with risk in [0, budget] and runs Dijkstra
// over that graph using the objective ordering (cost, hops, edge-ID
// sequence).
//
// Every state records the exact risk spent so far, so no dominance
// reasoning is involved at all, which makes it a good cross-check for
// the Pareto-label solver. Its memory grows with O(nodes * budget), so
// it is only practical for small inputs.
func SolveByStateExpansion(g *Graph, source, target, budget int) (Result, error) {
	if source < 0 || source >= g.NumNodes || target < 0 || target >= g.NumNodes {
		return Result{}, errBadEndpoint
	}
	if budget < 0 {
		return Result{}, fmt.Errorf("negative budget %d", budget)
	}

	best := make(map[expState]*expDist)
	prev := make(map[expState]expState)
	prevEdge := make(map[expState]int)

	pq := &expHeap{}
	heap.Init(pq)
	s0 := expState{node: source, risk: 0}
	best[s0] = &expDist{}
	heap.Push(pq, expItem{st: s0, d: best[s0]})

	for pq.Len() > 0 {
		it := heap.Pop(pq).(expItem)
		cur := best[it.st]
		if cur == nil || it.d.cost != cur.cost || it.d.hops != cur.hops || compareSeq(it.d.seq, cur.seq) != 0 {
			continue // stale queue entry
		}
		for _, e := range g.Adj[it.st.node] {
			nr := it.st.risk + e.Risk
			if nr > budget {
				continue
			}
			ns := expState{node: e.To, risk: nr}
			seq := make([]int, len(cur.seq)+1)
			copy(seq, cur.seq)
			seq[len(cur.seq)] = e.ID
			nd := &expDist{cost: cur.cost + e.Cost, hops: cur.hops + 1, seq: seq}
			if old, ok := best[ns]; !ok || betterExp(nd, old) {
				best[ns] = nd
				prev[ns] = it.st
				prevEdge[ns] = e.ID
				heap.Push(pq, expItem{st: ns, d: nd})
			}
		}
	}

	// Pick the best state at the target over all risk levels <= budget.
	var win expState
	var winD *expDist
	for r := 0; r <= budget; r++ {
		st := expState{node: target, risk: r}
		if d, ok := best[st]; ok && (winD == nil || betterExp(d, winD)) {
			win, winD = st, d
		}
	}
	if winD == nil {
		return Result{Found: false}, nil
	}

	// Reconstruct the edge sequence by walking parents backwards.
	var rev []int
	for st := win; st != s0; {
		id := prevEdge[st]
		rev = append(rev, id)
		st = prev[st]
	}
	seq := make([]int, len(rev))
	for i, id := range rev {
		seq[len(rev)-1-i] = id
	}
	return buildResult(g, &label{node: target, cost: winD.cost, risk: win.risk, hops: winD.hops, seq: seq}), nil
}
