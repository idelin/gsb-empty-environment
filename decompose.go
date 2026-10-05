package coreindex

import "container/heap"

// Decomposition is the result of a full k-core decomposition.
//
// Order is the deterministic peeling order: nodes are removed by
// increasing residual degree, ties broken by ascending node ID.
// RemDeg[i] is the residual degree of Order[i] at removal time, i.e.
// its degree in the graph induced by Order[i:]. Core values can be
// verified from Order and RemDeg alone via Consistent.
type Decomposition struct {
	Core   map[int64]int
	Order  []int64
	RemDeg []int
}

// peelItem is a heap entry keyed by (residual degree, node ID).
type peelItem struct {
	deg int
	id  int64
}

type peelHeap []peelItem

func (h peelHeap) Len() int { return len(h) }
func (h peelHeap) Less(i, j int) bool {
	if h[i].deg != h[j].deg {
		return h[i].deg < h[j].deg
	}
	return h[i].id < h[j].id
}
func (h peelHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *peelHeap) Push(x interface{}) { *h = append(*h, x.(peelItem)) }
func (h *peelHeap) Pop() interface{} {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

// Decompose runs the Batagelj-Zaversnik peeling algorithm on the
// current graph. It does not modify the maintained core numbers.
// The result is deterministic: equal residual degrees are ordered by
// ascending node ID. Isolated nodes are included (core 0).
func (ix *Index) Decompose() Decomposition {
	deg := make(map[int64]int, len(ix.adj))
	h := &peelHeap{}
	for id, nbrs := range ix.adj {
		deg[id] = len(nbrs)
		heap.Push(h, peelItem{deg: len(nbrs), id: id})
	}
	heap.Init(h)

	removed := make(map[int64]bool, len(ix.adj))
	d := Decomposition{Core: make(map[int64]int, len(ix.adj))}
	maxCore := 0
	for h.Len() > 0 {
		it := heap.Pop(h).(peelItem)
		if removed[it.id] || it.deg != deg[it.id] {
			continue // stale heap entry
		}
		removed[it.id] = true
		if it.deg > maxCore {
			maxCore = it.deg
		}
		d.Core[it.id] = maxCore
		d.Order = append(d.Order, it.id)
		d.RemDeg = append(d.RemDeg, it.deg)
		for w := range ix.adj[it.id] {
			if !removed[w] {
				deg[w]--
				heap.Push(h, peelItem{deg: deg[w], id: w})
			}
		}
	}
	return d
}

// Build recomputes the decomposition from scratch and installs the
// resulting core numbers as the maintained state. It returns the
// decomposition (peeling order and residual degrees for verification).
func (ix *Index) Build() Decomposition {
	d := ix.Decompose()
	ix.core = make(map[int64]int, len(d.Core))
	for id, c := range d.Core {
		ix.core[id] = c
	}
	return d
}

// Consistent verifies that Core is reproducible from Order and RemDeg:
// scanning the peeling order, each node's core equals the running
// maximum of residual degrees seen so far.
func (d Decomposition) Consistent() bool {
	if len(d.Order) != len(d.Core) || len(d.RemDeg) != len(d.Order) {
		return false
	}
	maxCore := 0
	for i, id := range d.Order {
		if d.RemDeg[i] > maxCore {
			maxCore = d.RemDeg[i]
		}
		if c, ok := d.Core[id]; !ok || c != maxCore {
			return false
		}
	}
	return true
}
