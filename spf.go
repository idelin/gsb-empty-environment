package sim

import "container/heap"

type spfItem struct {
	dist  int
	first int
	node  int
}

type spfHeap []spfItem

func (h spfHeap) Len() int { return len(h) }
func (h spfHeap) Less(i, j int) bool {
	if h[i].dist != h[j].dist {
		return h[i].dist < h[j].dist
	}
	if h[i].first != h[j].first {
		return h[i].first < h[j].first
	}
	return h[i].node < h[j].node
}
func (h spfHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *spfHeap) Push(x any)   { *h = append(*h, x.(spfItem)) }
func (h *spfHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

// computeRoutes runs Dijkstra over the local LSDB. Ties between
// equal-cost paths are broken deterministically by the smaller
// first-hop router ID, so the next hop is stable across recomputations
// as long as the topology is unchanged.
func (r *Router) computeRoutes() map[int]Route {
	dist := map[int]int{r.ID: 0}
	first := map[int]int{r.ID: r.ID}
	h := &spfHeap{{dist: 0, first: r.ID, node: r.ID}}
	for h.Len() > 0 {
		it := heap.Pop(h).(spfItem)
		if it.dist != dist[it.node] || it.first != first[it.node] {
			continue // stale heap entry
		}
		entry, ok := r.lsdb[it.node]
		if !ok {
			continue // no LSA from this node: cannot expand beyond it
		}
		for nbr, cost := range entry.lsa.Links {
			nd := it.dist + cost
			nf := it.first
			if it.node == r.ID {
				nf = nbr
			}
			cur, seen := dist[nbr]
			if !seen || nd < cur || (nd == cur && nf < first[nbr]) {
				dist[nbr] = nd
				first[nbr] = nf
				heap.Push(h, spfItem{dist: nd, first: nf, node: nbr})
			}
		}
	}
	routes := make(map[int]Route, len(dist)-1)
	for node, d := range dist {
		if node == r.ID {
			continue
		}
		routes[node] = Route{NextHop: first[node], Cost: d}
	}
	return routes
}
