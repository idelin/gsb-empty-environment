// Package kcore implements K-Core decomposition for large sparse
// undirected graphs: exact core-number computation, iterative peeling,
// layered output, and incremental (local) core maintenance under edge
// insertions and deletions.
package kcore

import "sort"

// Graph is an undirected simple graph stored as adjacency sets.
// Vertex IDs are arbitrary ints; self loops and parallel edges are
// rejected, which keeps degree counts exact for core decomposition.
type Graph struct {
	adj   map[int]map[int]struct{}
	edges int
}

// NewGraph returns an empty graph.
func NewGraph() *Graph {
	return &Graph{adj: make(map[int]map[int]struct{})}
}

// AddVertex inserts an isolated vertex if absent.
func (g *Graph) AddVertex(v int) {
	if _, ok := g.adj[v]; !ok {
		g.adj[v] = make(map[int]struct{})
	}
}

// AddEdge inserts edge (u,v). It reports whether the edge was new.
func (g *Graph) AddEdge(u, v int) bool {
	if u == v {
		return false
	}
	g.AddVertex(u)
	g.AddVertex(v)
	if _, ok := g.adj[u][v]; ok {
		return false
	}
	g.adj[u][v] = struct{}{}
	g.adj[v][u] = struct{}{}
	g.edges++
	return true
}

// RemoveEdge deletes edge (u,v). It reports whether the edge existed.
func (g *Graph) RemoveEdge(u, v int) bool {
	if _, ok := g.adj[u]; !ok {
		return false
	}
	if _, ok := g.adj[u][v]; !ok {
		return false
	}
	delete(g.adj[u], v)
	delete(g.adj[v], u)
	g.edges--
	return true
}

// HasEdge reports whether edge (u,v) exists.
func (g *Graph) HasEdge(u, v int) bool {
	if n, ok := g.adj[u]; ok {
		_, ok = n[v]
		return ok
	}
	return false
}

// Degree returns the number of edges incident to v.
func (g *Graph) Degree(v int) int { return len(g.adj[v]) }

// Neighbors returns the sorted neighbor list of v.
func (g *Graph) Neighbors(v int) []int {
	out := make([]int, 0, len(g.adj[v]))
	for w := range g.adj[v] {
		out = append(out, w)
	}
	sort.Ints(out)
	return out
}

// Vertices returns the sorted vertex list.
func (g *Graph) Vertices() []int {
	out := make([]int, 0, len(g.adj))
	for v := range g.adj {
		out = append(out, v)
	}
	sort.Ints(out)
	return out
}

// NumVertices returns |V|.
func (g *Graph) NumVertices() int { return len(g.adj) }

// NumEdges returns |E|.
func (g *Graph) NumEdges() int { return g.edges }
