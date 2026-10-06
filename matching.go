// Package matching computes maximum matchings in bipartite (assignment)
// graphs and identifies the edges that appear in every maximum matching.
//
// The input graph may contain duplicate edges between the same pair of
// vertices. Each input edge keeps its own id, but matching semantics are
// defined on vertex pairs: parallel edges are interchangeable, so the
// graph is deduplicated by (Left, Right) pair before any computation.
// Results reference the canonical (smallest) edge id of each pair.
package matching

import "sort"

// Edge is an input edge between a left and a right vertex.
type Edge struct {
	ID    int // input edge id, assigned in insertion order
	Left  int // index of the left vertex, 0 <= Left < LeftSize
	Right int // index of the right vertex, 0 <= Right < RightSize
}

// Graph is a bipartite graph with fixed vertex partitions.
type Graph struct {
	left  int
	right int
	edges []Edge

	// Pair-deduplicated view, rebuilt lazily by build().
	adj       [][]int        // adj[u] = deduped right neighbors of u
	canonical map[[2]int]int // (u,v) -> smallest edge id for the pair
	built     bool
}

// NewGraph returns an empty bipartite graph with the given partition sizes.
func NewGraph(left, right int) *Graph {
	if left < 0 || right < 0 {
		panic("matching: negative partition size")
	}
	return &Graph{left: left, right: right}
}

// AddEdge adds an edge and returns its id. Duplicate edges between the same
// vertex pair are allowed: they receive distinct ids but are treated as a
// single pair by all matching computations.
func (g *Graph) AddEdge(left, right int) int {
	if left < 0 || left >= g.left || right < 0 || right >= g.right {
		panic("matching: vertex out of range")
	}
	id := len(g.edges)
	g.edges = append(g.edges, Edge{ID: id, Left: left, Right: right})
	g.built = false
	return id
}

// Edges returns the input edges, duplicates included, in insertion order.
func (g *Graph) Edges() []Edge {
	out := make([]Edge, len(g.edges))
	copy(out, g.edges)
	return out
}

func (g *Graph) build() {
	if g.built {
		return
	}
	g.adj = make([][]int, g.left)
	g.canonical = make(map[[2]int]int, len(g.edges))
	for _, e := range g.edges {
		p := [2]int{e.Left, e.Right}
		if _, ok := g.canonical[p]; ok {
			continue
		}
		g.canonical[p] = e.ID
		g.adj[e.Left] = append(g.adj[e.Left], e.Right)
	}
	g.built = true
}

// MaxMatching returns a maximum-cardinality matching as a set of edges, one
// per matched vertex pair, using the canonical (smallest) id of each pair.
// The result is sorted by (Left, Right).
func (g *Graph) MaxMatching() []Edge {
	matchL, _ := g.hopcroftKarp()
	var out []Edge
	for u, v := range matchL {
		if v >= 0 {
			out = append(out, Edge{ID: g.canonical[[2]int{u, v}], Left: u, Right: v})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Left != out[j].Left {
			return out[i].Left < out[j].Left
		}
		return out[i].Right < out[j].Right
	})
	return out
}

// EssentialEdges returns the edges that appear in every maximum matching of
// the graph. Essentiality is a property of a vertex pair: if a pair is
// essential, its canonical edge is returned regardless of duplicates.
// The result is sorted by (Left, Right).
func (g *Graph) EssentialEdges() []Edge {
	matchL, matchR := g.hopcroftKarp()
	n, m := g.left, g.right
	size := n + m

	// Orient the pair-deduplicated graph: unmatched edges go left->right,
	// matched edges go right->right... matched edges go right->left.
	fwd := make([][]int, size)
	rev := make([][]int, size)
	add := func(a, b int) {
		fwd[a] = append(fwd[a], b)
		rev[b] = append(rev[b], a)
	}
	for u := 0; u < n; u++ {
		for _, v := range g.adj[u] {
			if matchL[u] == v {
				add(n+v, u)
			} else {
				add(u, n+v)
			}
		}
	}

	comp := kosaraju(fwd, rev)

	// Vertices reachable from free left vertices (even alternating paths).
	var freeL, freeR []int
	for u := 0; u < n; u++ {
		if matchL[u] < 0 {
			freeL = append(freeL, u)
		}
	}
	for v := 0; v < m; v++ {
		if matchR[v] < 0 {
			freeR = append(freeR, n+v)
		}
	}
	fromFreeL := reachable(fwd, freeL)
	// Vertices that can reach a free right vertex.
	toFreeR := reachable(rev, freeR)

	var out []Edge
	for u := 0; u < n; u++ {
		v := matchL[u]
		if v < 0 {
			continue
		}
		switch {
		case comp[u] == comp[n+v]:
			// On an alternating cycle: flipping the cycle drops the edge.
		case fromFreeL[n+v]:
			// Even alternating path from a free left vertex ends at v.
		case toFreeR[u]:
			// Even alternating path from u reaches a free right vertex.
		default:
			out = append(out, Edge{ID: g.canonical[[2]int{u, v}], Left: u, Right: v})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Left != out[j].Left {
			return out[i].Left < out[j].Left
		}
		return out[i].Right < out[j].Right
	})
	return out
}

// hopcroftKarp computes a maximum matching over the deduplicated pairs.
// It returns matchL[u] = v (or -1) and matchR[v] = u (or -1).
func (g *Graph) hopcroftKarp() ([]int, []int) {
	g.build()
	n, m := g.left, g.right
	matchL := make([]int, n)
	matchR := make([]int, m)
	for i := range matchL {
		matchL[i] = -1
	}
	for i := range matchR {
		matchR[i] = -1
	}
	dist := make([]int, n)

	bfs := func() bool {
		queue := make([]int, 0, n)
		found := false
		for u := 0; u < n; u++ {
			if matchL[u] < 0 {
				dist[u] = 0
				queue = append(queue, u)
			} else {
				dist[u] = -1
			}
		}
		for len(queue) > 0 {
			u := queue[0]
			queue = queue[1:]
			for _, v := range g.adj[u] {
				w := matchR[v]
				if w < 0 {
					found = true
				} else if dist[w] < 0 {
					dist[w] = dist[u] + 1
					queue = append(queue, w)
				}
			}
		}
		return found
	}

	var dfs func(u int) bool
	dfs = func(u int) bool {
		for _, v := range g.adj[u] {
			w := matchR[v]
			if w < 0 || (dist[w] == dist[u]+1 && dfs(w)) {
				matchL[u] = v
				matchR[v] = u
				return true
			}
		}
		dist[u] = -1
		return false
	}

	for bfs() {
		for u := 0; u < n; u++ {
			if matchL[u] < 0 {
				dfs(u)
			}
		}
	}
	return matchL, matchR
}

// kosaraju returns the strongly connected components of a directed graph
// given by forward and reverse adjacency lists. comp[v] is a component id.
func kosaraju(fwd, rev [][]int) []int {
	size := len(fwd)
	visited := make([]bool, size)
	order := make([]int, 0, size)

	// Iterative post-order DFS on fwd.
	for s := 0; s < size; s++ {
		if visited[s] {
			continue
		}
		type frame struct{ v, i int }
		stack := []frame{{s, 0}}
		visited[s] = true
		for len(stack) > 0 {
			f := &stack[len(stack)-1]
			if f.i < len(fwd[f.v]) {
				w := fwd[f.v][f.i]
				f.i++
				if !visited[w] {
					visited[w] = true
					stack = append(stack, frame{w, 0})
				}
			} else {
				order = append(order, f.v)
				stack = stack[:len(stack)-1]
			}
		}
	}

	comp := make([]int, size)
	for i := range comp {
		comp[i] = -1
	}
	id := 0
	for i := size - 1; i >= 0; i-- {
		s := order[i]
		if comp[s] >= 0 {
			continue
		}
		stack := []int{s}
		comp[s] = id
		for len(stack) > 0 {
			v := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for _, w := range rev[v] {
				if comp[w] < 0 {
					comp[w] = id
					stack = append(stack, w)
				}
			}
		}
		id++
	}
	return comp
}

// reachable marks all vertices reachable from any source via BFS.
func reachable(adj [][]int, sources []int) []bool {
	seen := make([]bool, len(adj))
	queue := make([]int, 0, len(sources))
	for _, s := range sources {
		if !seen[s] {
			seen[s] = true
			queue = append(queue, s)
		}
	}
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		for _, w := range adj[v] {
			if !seen[w] {
				seen[w] = true
				queue = append(queue, w)
			}
		}
	}
	return seen
}
