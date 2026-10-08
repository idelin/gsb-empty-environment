// Package graph models a road network as a directed multigraph with
// non-negative integer weights. Parallel arcs between the same pair of
// nodes are distinguished by a unique, stable edge ID.
package graph

import "container/heap"

// NoEdge is the sentinel edge ID meaning "no edge is banned".
const NoEdge = -1

// Edge is a single directed arc with a non-negative integer weight.
type Edge struct {
	ID     int
	From   int
	To     int
	Weight int64
}

// Graph is a directed multigraph. Edge IDs are assigned sequentially at
// insertion time and never change.
type Graph struct {
	NumNodes int
	Edges    []Edge
	adj      [][]int // node -> IDs of outgoing edges
}

// New returns an empty graph with numNodes nodes numbered 0..numNodes-1.
func New(numNodes int) *Graph {
	if numNodes < 0 {
		panic("graph: negative node count")
	}
	return &Graph{NumNodes: numNodes, adj: make([][]int, numNodes)}
}

// AddEdge appends a directed edge and returns its unique ID. Parallel
// edges (same From/To) each get their own ID. weight must be >= 0.
func (g *Graph) AddEdge(from, to int, weight int64) int {
	if weight < 0 {
		panic("graph: negative edge weight")
	}
	if from < 0 || from >= g.NumNodes || to < 0 || to >= g.NumNodes {
		panic("graph: node out of range")
	}
	id := len(g.Edges)
	g.Edges = append(g.Edges, Edge{ID: id, From: from, To: to, Weight: weight})
	g.adj[from] = append(g.adj[from], id)
	return id
}

// Result is the outcome of a shortest path query. When Reachable is false,
// Dist and Path are meaningless and the target is unreachable.
type Result struct {
	Dist      int64
	Reachable bool
	Path      []int // edge IDs in s -> t order; nil when unreachable or s == t
}

// ShortestPath runs Dijkstra from s to t, skipping the edge with ID banned
// (pass NoEdge to use the full graph). Only that exact edge is removed;
// parallel edges between the same nodes remain usable.
//
// Zero-weight edges and zero-weight cycles are handled naturally. When
// several shortest paths of equal length exist, an arbitrary one is
// returned (the search is deterministic for a fixed graph).
func (g *Graph) ShortestPath(s, t, banned int) Result {
	if s < 0 || s >= g.NumNodes || t < 0 || t >= g.NumNodes {
		panic("graph: node out of range")
	}
	const inf = int64(-1) // sentinel: distance unknown
	dist := make([]int64, g.NumNodes)
	prevNode := make([]int, g.NumNodes)
	prevEdge := make([]int, g.NumNodes)
	for i := range dist {
		dist[i] = inf
		prevNode[i] = -1
		prevEdge[i] = -1
	}
	dist[s] = 0

	pq := &nodeHeap{{node: s, dist: 0}}
	heap.Init(pq)
	done := make([]bool, g.NumNodes)
	for pq.Len() > 0 {
		item := heap.Pop(pq).(nodeItem)
		u := item.node
		if done[u] {
			continue
		}
		done[u] = true
		if u == t {
			break
		}
		for _, id := range g.adj[u] {
			if id == banned {
				continue
			}
			e := g.Edges[id]
			nd := dist[u] + e.Weight
			if dist[e.To] == inf || nd < dist[e.To] {
				dist[e.To] = nd
				prevNode[e.To] = u
				prevEdge[e.To] = id
				heap.Push(pq, nodeItem{node: e.To, dist: nd})
			}
		}
	}

	if dist[t] == inf {
		return Result{Reachable: false}
	}
	// Reconstruct the path by walking predecessors from t back to s.
	var rev []int
	for v := t; v != s; v = prevNode[v] {
		rev = append(rev, prevEdge[v])
	}
	path := make([]int, len(rev))
	for i, id := range rev {
		path[len(rev)-1-i] = id
	}
	return Result{Dist: dist[t], Reachable: true, Path: path}
}

// PathWeight returns the total weight of a path given as edge IDs and
// verifies that the edges chain from s to t. ok is false if any edge ID is
// invalid or the chain is broken.
func (g *Graph) PathWeight(s, t int, path []int) (weight int64, ok bool) {
	cur := s
	for _, id := range path {
		if id < 0 || id >= len(g.Edges) {
			return 0, false
		}
		e := g.Edges[id]
		if e.From != cur {
			return 0, false
		}
		weight += e.Weight
		cur = e.To
	}
	return weight, cur == t
}

type nodeItem struct {
	node int
	dist int64
}

type nodeHeap []nodeItem

func (h nodeHeap) Len() int            { return len(h) }
func (h nodeHeap) Less(i, j int) bool  { return h[i].dist < h[j].dist }
func (h nodeHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *nodeHeap) Push(x interface{}) { *h = append(*h, x.(nodeItem)) }
func (h *nodeHeap) Pop() interface{} {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}
