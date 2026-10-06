// Package edgerouter finds minimum-total-time routes on a directed road
// network where edges have travel times, some consecutive edge pairs are
// forbidden (turn restrictions), and turns incur waiting times.
//
// Because the cost and feasibility of the next move depend on the edge you
// arrive on, the routing state is the arrival edge, not the vertex. A
// vertex-based shortest-distance table would incorrectly merge states that
// share a vertex but differ in the incoming edge.
package edgerouter

import (
	"container/heap"
	"sort"
)

// Edge is a directed road segment with a traversal time.
type Edge struct {
	ID   int
	From int
	To   int
	Time int64
}

// Graph is a road network with turn restrictions and turn waiting times.
type Graph struct {
	edges     map[int]Edge
	out       map[int][]int // vertex -> outgoing edge IDs, sorted
	turnWait  map[[2]int]int64
	forbidden map[[2]int]bool
}

// NewGraph builds a Graph from directed edges. Edge IDs must be unique.
func NewGraph(edges []Edge) *Graph {
	g := &Graph{
		edges:     make(map[int]Edge, len(edges)),
		out:       make(map[int][]int),
		turnWait:  make(map[[2]int]int64),
		forbidden: make(map[[2]int]bool),
	}
	for _, e := range edges {
		if _, dup := g.edges[e.ID]; dup {
			panic("edgerouter: duplicate edge ID")
		}
		g.edges[e.ID] = e
		g.out[e.From] = append(g.out[e.From], e.ID)
	}
	for _, ids := range g.out {
		sort.Ints(ids)
	}
	return g
}

// SetTurnWait sets the waiting time for turning from edge prev onto edge next.
func (g *Graph) SetTurnWait(prev, next int, wait int64) {
	g.turnWait[[2]int{prev, next}] = wait
}

// ForbidTurn forbids turning from edge prev onto edge next.
func (g *Graph) ForbidTurn(prev, next int) {
	g.forbidden[[2]int{prev, next}] = true
}

// transition reports whether turning from prev onto next is allowed and its
// extra cost: the turn waiting time plus the traversal time of next.
func (g *Graph) transition(prev, next int) (int64, bool) {
	if g.forbidden[[2]int{prev, next}] {
		return 0, false
	}
	e, ok := g.edges[next]
	if !ok {
		return 0, false
	}
	return g.turnWait[[2]int{prev, next}] + e.Time, true
}

// Path is a route: total time and the traversed edge IDs in order.
type Path struct {
	Total int64
	Edges []int
}

// compareSeq compares two edge-ID sequences lexicographically; a strict
// prefix is smaller than its extension.
func compareSeq(a, b []int) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	}
	return 0
}

// lessPath orders paths by total time, then by edge-ID sequence.
func lessPath(costA int64, seqA []int, costB int64, seqB []int) bool {
	if costA != costB {
		return costA < costB
	}
	return compareSeq(seqA, seqB) < 0
}

// queueItem is a Dijkstra frontier entry keyed by (cost, edge sequence).
type queueItem struct {
	cost int64
	seq  []int
	edge int // arrival edge = current state
}

type priorityQueue []queueItem

func (pq priorityQueue) Len() int { return len(pq) }

func (pq priorityQueue) Less(i, j int) bool {
	if pq[i].cost != pq[j].cost {
		return pq[i].cost < pq[j].cost
	}
	if c := compareSeq(pq[i].seq, pq[j].seq); c != 0 {
		return c < 0
	}
	return pq[i].edge < pq[j].edge
}

func (pq priorityQueue) Swap(i, j int) { pq[i], pq[j] = pq[j], pq[i] }

func (pq *priorityQueue) Push(x interface{}) {
	*pq = append(*pq, x.(queueItem))
}

func (pq *priorityQueue) Pop() interface{} {
	old := *pq
	n := len(old)
	item := old[n-1]
	*pq = old[:n-1]
	return item
}

// ShortestPath returns the minimum-total-time path from src to dst, breaking
// ties by the lexicographically smallest edge-ID sequence. The state is the
// arrival edge, so the same vertex may be revisited via different incoming
// edges, but a state (edge) is never repeated, which keeps zero-cost cycles
// from looping. The second return value reports whether a path exists.
func (g *Graph) ShortestPath(src, dst int) (Path, bool) {
	if src == dst {
		return Path{Total: 0, Edges: nil}, true
	}

	best := make(map[int]queueItem) // arrival edge -> best known label
	pq := &priorityQueue{}
	heap.Init(pq)

	for _, id := range g.out[src] {
		e := g.edges[id]
		item := queueItem{cost: e.Time, seq: []int{id}, edge: id}
		best[id] = item
		heap.Push(pq, item)
	}

	done := make(map[int]bool)
	for pq.Len() > 0 {
		item := heap.Pop(pq).(queueItem)
		if done[item.edge] {
			continue
		}
		done[item.edge] = true
		if g.edges[item.edge].To == dst {
			// First finalized state ending at dst is globally optimal:
			// labels pop in non-decreasing (cost, seq) order.
			return Path{Total: item.cost, Edges: item.seq}, true
		}
		for _, next := range g.out[g.edges[item.edge].To] {
			if done[next] {
				continue
			}
			extra, ok := g.transition(item.edge, next)
			if !ok {
				continue
			}
			ncost := item.cost + extra
			nseq := make([]int, len(item.seq)+1)
			copy(nseq, item.seq)
			nseq[len(item.seq)] = next
			if cur, seen := best[next]; !seen || lessPath(ncost, nseq, cur.cost, cur.seq) {
				best[next] = queueItem{cost: ncost, seq: nseq, edge: next}
				heap.Push(pq, queueItem{cost: ncost, seq: nseq, edge: next})
			}
		}
	}
	return Path{}, false
}
