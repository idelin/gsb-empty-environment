package coreidx

import "container/heap"

// PeelStep describes one step of the peeling (degeneracy) ordering.
type PeelStep struct {
	Node int64
	// Degree is the number of not-yet-peeled neighbors at the moment Node
	// was peeled (its "remaining degree").
	Degree int
	// Core is the core number assigned to Node.
	Core int
}

// BuildResult is the outcome of a full k-core decomposition.
type BuildResult struct {
	// Steps lists nodes in deterministic peeling order. Ties between nodes
	// with equal remaining degree are broken by ascending node ID.
	Steps []PeelStep
}

// Order returns the peeling order (node IDs) implied by the result.
func (r BuildResult) Order() []int64 {
	order := make([]int64, len(r.Steps))
	for i, s := range r.Steps {
		order[i] = s.Node
	}
	return order
}

// Cores returns the core number of every node.
func (r BuildResult) Cores() map[int64]int {
	cores := make(map[int64]int, len(r.Steps))
	for _, s := range r.Steps {
		cores[s.Node] = s.Core
	}
	return cores
}

// peelItem is a heap entry keyed by (remaining degree, node ID).
type peelItem struct {
	node int64
	deg  int
}

type peelHeap []peelItem

func (h peelHeap) Len() int { return len(h) }
func (h peelHeap) Less(i, j int) bool {
	if h[i].deg != h[j].deg {
		return h[i].deg < h[j].deg
	}
	return h[i].node < h[j].node
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

// Build computes the core number of every node with the classic
// Batagelj-Zaversnik peeling algorithm and stores them in the graph, so
// that ApplyBatch can maintain them incrementally afterwards.
//
// The peeling is deterministic: among the nodes with the smallest remaining
// degree the smallest node ID is peeled first. Isolated nodes are peeled
// with degree 0 and core 0, and remain part of the graph.
//
// For every peeled node v the result records its remaining degree d(v).
// The values allow independent verification: with nodes v1..vn in peeling
// order, core(vi) = max(d(vi), max{ core(vj) : j < i and vj adjacent vi }).
func (g *Graph) Build() BuildResult {
	deg := make(map[int64]int, len(g.adj))
	maxPrevCore := make(map[int64]int, len(g.adj))
	h := &peelHeap{}
	for id, nbrs := range g.adj {
		deg[id] = len(nbrs)
		heap.Push(h, peelItem{node: id, deg: len(nbrs)})
	}

	removed := make(map[int64]bool, len(g.adj))
	res := BuildResult{Steps: make([]PeelStep, 0, len(g.adj))}
	g.core = make(map[int64]int, len(g.adj))

	for h.Len() > 0 {
		it := heap.Pop(h).(peelItem)
		if removed[it.node] || it.deg != deg[it.node] {
			continue // stale heap entry
		}
		removed[it.node] = true
		c := it.deg
		if maxPrevCore[it.node] > c {
			c = maxPrevCore[it.node]
		}
		g.core[it.node] = c
		res.Steps = append(res.Steps, PeelStep{Node: it.node, Degree: it.deg, Core: c})
		for nb := range g.adj[it.node] {
			if removed[nb] {
				continue
			}
			deg[nb]--
			if c > maxPrevCore[nb] {
				maxPrevCore[nb] = c
			}
			heap.Push(h, peelItem{node: nb, deg: deg[nb]})
		}
	}

	g.built = true
	return res
}
