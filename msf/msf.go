package main

import "sort"

// Edge is a weighted undirected edge with a stable, unique ID.
type Edge struct {
	ID   int
	U, V int
	W    int64
}

// lessEdge orders edges by (weight, ID): ties on weight are broken by ID.
func lessEdge(a, b Edge) bool {
	if a.W != b.W {
		return a.W < b.W
	}
	return a.ID < b.ID
}

// DSU is a disjoint-set union structure.
type DSU struct {
	parent []int
	rank   []int
}

func NewDSU(n int) *DSU {
	p := make([]int, n)
	for i := range p {
		p[i] = i
	}
	return &DSU{parent: p, rank: make([]int, n)}
}

func (d *DSU) Find(x int) int {
	for d.parent[x] != x {
		d.parent[x] = d.parent[d.parent[x]]
		x = d.parent[x]
	}
	return x
}

func (d *DSU) Union(a, b int) bool {
	ra, rb := d.Find(a), d.Find(b)
	if ra == rb {
		return false
	}
	if d.rank[ra] < d.rank[rb] {
		ra, rb = rb, ra
	}
	d.parent[rb] = ra
	if d.rank[ra] == d.rank[rb] {
		d.rank[ra]++
	}
	return true
}

// Result holds the minimum spanning forest analysis.
type Result struct {
	Total       int64          // total weight of the MSF
	TreeEdges   []Edge         // MSF edges, sorted by ID
	Replacement map[int]*Edge  // tree edge ID -> cheapest replacement, nil = disconnected
}

// Analyze computes the minimum spanning forest via Kruskal with (weight, ID)
// ordering, then for every tree edge finds the cheapest non-tree edge that
// reconnects the cut created by removing it.
func Analyze(n int, edges []Edge) Result {
	sorted := make([]Edge, len(edges))
	copy(sorted, edges)
	sort.Slice(sorted, func(i, j int) bool { return lessEdge(sorted[i], sorted[j]) })

	dsu := NewDSU(n)
	inTree := make(map[int]bool)
	var total int64
	for _, e := range sorted {
		if e.U == e.V {
			continue // self-loops never belong to a forest
		}
		if dsu.Union(e.U, e.V) {
			inTree[e.ID] = true
			total += e.W
		}
	}

	// Forest adjacency (stores indices into edges).
	adj := make([][]int, n)
	for i, e := range edges {
		if inTree[e.ID] {
			adj[e.U] = append(adj[e.U], i)
			adj[e.V] = append(adj[e.V], i)
		}
	}

	// Root every tree of the forest; record parent and the edge to the parent.
	depth := make([]int, n)
	parent := make([]int, n)
	parentEdge := make([]int, n)
	visited := make([]bool, n)
	for s := 0; s < n; s++ {
		if visited[s] {
			continue
		}
		visited[s] = true
		parent[s] = -1
		parentEdge[s] = -1
		stack := []int{s}
		for len(stack) > 0 {
			u := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for _, ei := range adj[u] {
				e := edges[ei]
				v := e.U
				if v == u {
					v = e.V
				}
				if visited[v] {
					continue
				}
				visited[v] = true
				depth[v] = depth[u] + 1
				parent[v] = u
				parentEdge[v] = e.ID
				stack = append(stack, v)
			}
		}
	}

	// Every non-tree edge (u,v) is a replacement candidate for exactly the
	// tree edges on the u-v path in its tree. Walk each path and keep the
	// best candidate per tree edge.
	best := make(map[int]Edge)
	has := make(map[int]bool)
	for _, e := range edges {
		if inTree[e.ID] || e.U == e.V {
			continue
		}
		u, v := e.U, e.V
		for u != v {
			if depth[u] < depth[v] {
				u, v = v, u
			}
			pe := parentEdge[u]
			if !has[pe] || lessEdge(e, best[pe]) {
				best[pe] = e
				has[pe] = true
			}
			u = parent[u]
		}
	}

	var treeEdges []Edge
	for _, e := range edges {
		if inTree[e.ID] {
			treeEdges = append(treeEdges, e)
		}
	}
	sort.Slice(treeEdges, func(i, j int) bool { return treeEdges[i].ID < treeEdges[j].ID })

	repl := make(map[int]*Edge, len(treeEdges))
	for _, e := range treeEdges {
		if has[e.ID] {
			r := best[e.ID]
			repl[e.ID] = &r
		} else {
			repl[e.ID] = nil
		}
	}
	return Result{Total: total, TreeEdges: treeEdges, Replacement: repl}
}
