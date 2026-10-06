// Package roadgraph provides shortest-path tooling for directed multigraphs
// with non-negative integer weights, replacement-path computation, and
// versioned/time-bounded graphs with a dependency-aware path cache.
package roadgraph

import (
	"container/heap"
	"fmt"
	"math"
)

// EdgeID uniquely identifies an edge. Parallel edges between the same pair
// of nodes are distinguished solely by their IDs.
type EdgeID int64

// Inf is a sentinel distance larger than any attainable path length.
const Inf = math.MaxInt64 / 4

// Edge is a single directed weighted edge.
type Edge struct {
	ID       EdgeID
	From, To int
	Weight   int64 // non-negative
}

// Graph is a directed multigraph with non-negative integer weights.
type Graph struct {
	NumNodes int
	adj      [][]*Edge
	byID     map[EdgeID]*Edge
}

// NewGraph returns an empty graph with numNodes nodes numbered 0..numNodes-1.
func NewGraph(numNodes int) *Graph {
	if numNodes < 0 {
		numNodes = 0
	}
	return &Graph{
		NumNodes: numNodes,
		adj:      make([][]*Edge, numNodes),
		byID:     make(map[EdgeID]*Edge),
	}
}

// AddEdge adds an edge. IDs must be unique; weights must be non-negative.
func (g *Graph) AddEdge(id EdgeID, from, to int, weight int64) error {
	if weight < 0 {
		return fmt.Errorf("edge %d: negative weight %d", id, weight)
	}
	if from < 0 || from >= g.NumNodes || to < 0 || to >= g.NumNodes {
		return fmt.Errorf("edge %d: endpoint out of range", id)
	}
	if _, dup := g.byID[id]; dup {
		return fmt.Errorf("edge %d: duplicate id", id)
	}
	e := &Edge{ID: id, From: from, To: to, Weight: weight}
	g.byID[id] = e
	g.adj[from] = append(g.adj[from], e)
	return nil
}

// Edge returns the edge with the given id.
func (g *Graph) Edge(id EdgeID) (*Edge, bool) {
	e, ok := g.byID[id]
	return e, ok
}

// Edges returns all edges (in unspecified order).
func (g *Graph) Edges() []*Edge {
	out := make([]*Edge, 0, len(g.byID))
	for _, e := range g.byID {
		out = append(out, e)
	}
	return out
}

// ShortestResult holds the output of a single-source Dijkstra run.
type ShortestResult struct {
	Dist     []int64         // Inf when unreachable
	PrevEdge []EdgeID        // edge used to enter the node, -1 for source/unreached
	PrevNode []int           // node before this one on the shortest tree, -1 for source/unreached
	Read     map[EdgeID]bool // every edge examined while relaxing (the read set)
}

type queueItem struct {
	node int
	dist int64
}

type distQueue []queueItem

func (q distQueue) Len() int            { return len(q) }
func (q distQueue) Less(i, j int) bool  { return q[i].dist < q[j].dist }
func (q distQueue) Swap(i, j int)       { q[i], q[j] = q[j], q[i] }
func (q *distQueue) Push(x interface{}) { *q = append(*q, x.(queueItem)) }
func (q *distQueue) Pop() interface{} {
	old := *q
	n := len(old)
	it := old[n-1]
	*q = old[:n-1]
	return it
}

// ShortestPaths runs Dijkstra from source, skipping edges in exclude.
// It never exits early, so Read contains every edge whose tail is reachable
// from source; an edge outside the read set cannot influence any shortest
// path from source.
func (g *Graph) ShortestPaths(source int, exclude map[EdgeID]bool) *ShortestResult {
	dist := make([]int64, g.NumNodes)
	prevEdge := make([]EdgeID, g.NumNodes)
	prevNode := make([]int, g.NumNodes)
	for i := range dist {
		dist[i] = Inf
		prevEdge[i] = -1
		prevNode[i] = -1
	}
	read := make(map[EdgeID]bool)
	dist[source] = 0
	pq := &distQueue{{node: source, dist: 0}}
	heap.Init(pq)
	for pq.Len() > 0 {
		it := heap.Pop(pq).(queueItem)
		if it.dist > dist[it.node] {
			continue
		}
		for _, e := range g.adj[it.node] {
			if exclude[e.ID] {
				continue
			}
			read[e.ID] = true
			if nd := it.dist + e.Weight; nd < dist[e.To] {
				dist[e.To] = nd
				prevEdge[e.To] = e.ID
				prevNode[e.To] = it.node
				heap.Push(pq, queueItem{node: e.To, dist: nd})
			}
		}
	}
	return &ShortestResult{Dist: dist, PrevEdge: prevEdge, PrevNode: prevNode, Read: read}
}

// PathTo reconstructs one shortest path to target as a list of edge IDs.
// The second return value is false when target is unreachable.
func (sr *ShortestResult) PathTo(target int) ([]EdgeID, bool) {
	if target < 0 || target >= len(sr.Dist) || sr.Dist[target] >= Inf {
		return nil, false
	}
	var rev []EdgeID
	for cur := target; sr.PrevEdge[cur] >= 0; cur = sr.PrevNode[cur] {
		rev = append(rev, sr.PrevEdge[cur])
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev, true
}

// ShortestPath returns the shortest distance from source to target and one
// witness path. reachable is false (and distance Inf) when none exists.
func (g *Graph) ShortestPath(source, target int, exclude map[EdgeID]bool) (dist int64, path []EdgeID, reachable bool) {
	sr := g.ShortestPaths(source, exclude)
	p, ok := sr.PathTo(target)
	return sr.Dist[target], p, ok
}
