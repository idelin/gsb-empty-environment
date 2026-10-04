// Package ssp computes single-source shortest paths in directed graphs
// that may contain negative edge weights, parallel edges, self-loops and
// negative cycles. For every (source, target) pair it reports one of:
// finite distance, unreachable, or "negative infinity" when the target is
// affected by a negative cycle that is reachable from the source and can
// itself reach the target.
package ssp

// Edge is a single directed weighted edge. Parallel edges and self-loops
// are allowed and kept as distinct entries.
type Edge struct {
	From, To int
	Weight   float64
}

// Graph is a directed weighted graph with string vertex names.
type Graph struct {
	Names []string
	index map[string]int
	Edges []Edge
	Adj   [][]int // Adj[v]: indices of edges leaving v
	Radj  [][]int // Radj[v]: indices of edges entering v
}

// NewGraph returns an empty graph.
func NewGraph() *Graph {
	return &Graph{index: make(map[string]int)}
}

// Vertex returns the id of the named vertex, creating it if needed.
func (g *Graph) Vertex(name string) int {
	if id, ok := g.index[name]; ok {
		return id
	}
	id := len(g.Names)
	g.index[name] = id
	g.Names = append(g.Names, name)
	g.Adj = append(g.Adj, nil)
	g.Radj = append(g.Radj, nil)
	return id
}

// Lookup returns the id of a vertex and whether it exists.
func (g *Graph) Lookup(name string) (int, bool) {
	id, ok := g.index[name]
	return id, ok
}

// AddEdge appends a directed edge. Parallel edges are preserved.
func (g *Graph) AddEdge(from, to string, w float64) int {
	u, v := g.Vertex(from), g.Vertex(to)
	ei := len(g.Edges)
	g.Edges = append(g.Edges, Edge{From: u, To: v, Weight: w})
	g.Adj[u] = append(g.Adj[u], ei)
	g.Radj[v] = append(g.Radj[v], ei)
	return ei
}

// NumVertices returns the vertex count.
func (g *Graph) NumVertices() int { return len(g.Names) }

// Reachable returns the set of vertices reachable from start (inclusive)
// following edge directions, computed by BFS.
func (g *Graph) Reachable(start int) []bool {
	seen := make([]bool, len(g.Names))
	if start < 0 || start >= len(g.Names) {
		return seen
	}
	queue := []int{start}
	seen[start] = true
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		for _, ei := range g.Adj[u] {
			v := g.Edges[ei].To
			if !seen[v] {
				seen[v] = true
				queue = append(queue, v)
			}
		}
	}
	return seen
}

// CanReach returns the set of vertices that can reach target (inclusive),
// computed by BFS on reversed edges.
func (g *Graph) CanReach(target int) []bool {
	seen := make([]bool, len(g.Names))
	if target < 0 || target >= len(g.Names) {
		return seen
	}
	queue := []int{target}
	seen[target] = true
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		for _, ei := range g.Radj[u] {
			v := g.Edges[ei].From
			if !seen[v] {
				seen[v] = true
				queue = append(queue, v)
			}
		}
	}
	return seen
}

// BFSPath returns a simple path (vertex ids, both endpoints included) from
// from to to, or nil if to is unreachable. Edge directions are followed.
func (g *Graph) BFSPath(from, to int) []int {
	n := len(g.Names)
	prev := make([]int, n)
	for i := range prev {
		prev[i] = -1
	}
	seen := make([]bool, n)
	seen[from] = true
	queue := []int{from}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		if u == to {
			break
		}
		for _, ei := range g.Adj[u] {
			v := g.Edges[ei].To
			if !seen[v] {
				seen[v] = true
				prev[v] = u
				queue = append(queue, v)
			}
		}
	}
	if !seen[to] {
		return nil
	}
	var rev []int
	for v := to; v != -1; v = prev[v] {
		rev = append(rev, v)
	}
	path := make([]int, len(rev))
	for i, v := range rev {
		path[len(rev)-1-i] = v
	}
	return path
}

// MinEdgeWeight returns the minimum weight among parallel edges u->v and
// whether any such edge exists.
func (g *Graph) MinEdgeWeight(u, v int) (float64, bool) {
	found := false
	var best float64
	for _, ei := range g.Adj[u] {
		e := g.Edges[ei]
		if e.To == v && (!found || e.Weight < best) {
			best = e.Weight
			found = true
		}
	}
	return best, found
}
