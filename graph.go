package mapmatch

import (
	"container/heap"
	"math"
)

// Edge is a directed-agnostic road segment between two graph nodes.
// Line is the road centerline polyline from node From to node To.
type Edge struct {
	ID     int
	Name   string
	From   int
	To     int
	Line   []Point
	Length float64 // centerline length in meters
}

// Graph is an undirected road network.
type Graph struct {
	Nodes []Point
	Edges []*Edge
	adj   map[int][]*Edge
}

// NewGraph creates a road network with the given node positions.
func NewGraph(nodes []Point) *Graph {
	return &Graph{Nodes: nodes, adj: make(map[int][]*Edge)}
}

// AddEdge adds a road segment between nodes from and to. line must start at
// node from and end at node to (it may contain intermediate shape points).
func (g *Graph) AddEdge(name string, from, to int, line []Point) *Edge {
	e := &Edge{
		ID:     len(g.Edges),
		Name:   name,
		From:   from,
		To:     to,
		Line:   line,
		Length: polylineLength(line),
	}
	g.Edges = append(g.Edges, e)
	g.adj[from] = append(g.adj[from], e)
	g.adj[to] = append(g.adj[to], e)
	return e
}

// dijkstra computes shortest-path distances in meters from src to all nodes.
func (g *Graph) dijkstra(src int) []float64 {
	dist := make([]float64, len(g.Nodes))
	for i := range dist {
		dist[i] = math.Inf(1)
	}
	dist[src] = 0
	pq := &distHeap{{node: src, dist: 0}}
	heap.Init(pq)
	for pq.Len() > 0 {
		cur := heap.Pop(pq).(distItem)
		if cur.dist > dist[cur.node] {
			continue
		}
		for _, e := range g.adj[cur.node] {
			next := e.From
			if next == cur.node {
				next = e.To
			}
			if nd := cur.dist + e.Length; nd < dist[next] {
				dist[next] = nd
				heap.Push(pq, distItem{node: next, dist: nd})
			}
		}
	}
	return dist
}

type distItem struct {
	node int
	dist float64
}

type distHeap []distItem

func (h distHeap) Len() int            { return len(h) }
func (h distHeap) Less(i, j int) bool  { return h[i].dist < h[j].dist }
func (h distHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *distHeap) Push(x interface{}) { *h = append(*h, x.(distItem)) }
func (h *distHeap) Pop() interface{} {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}
