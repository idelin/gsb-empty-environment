package main

import "sort"

// Edge is an undirected weighted edge with a stable, unique ID.
type Edge struct {
	ID int
	U  int
	V  int
	W  int
}

// lessEdge orders edges by (weight, id): the canonical tie-break.
func lessEdge(a, b Edge) bool {
	if a.W != b.W {
		return a.W < b.W
	}
	return a.ID < b.ID
}

type dsu struct {
	parent []int
}

func newDSU(n int) *dsu {
	p := make([]int, n)
	for i := range p {
		p[i] = i
	}
	return &dsu{parent: p}
}

func (d *dsu) find(x int) int {
	for d.parent[x] != x {
		d.parent[x] = d.parent[d.parent[x]]
		x = d.parent[x]
	}
	return x
}

func (d *dsu) union(a, b int) bool {
	ra, rb := d.find(a), d.find(b)
	if ra == rb {
		return false
	}
	d.parent[ra] = rb
	return true
}

// Analysis holds the MSF and, for every tree edge, its cheapest replacement.
type Analysis struct {
	Components  int
	TotalWeight int
	TreeEdges   []Edge        // sorted by edge ID
	Replacement map[int]*Edge // tree edge ID -> replacement edge, nil = disconnected
}

// Analyze computes the canonical minimum spanning forest (Kruskal ordered by
// (weight, id)) and the cheapest replacement edge for every tree edge.
func Analyze(n int, edges []Edge) Analysis {
	sorted := make([]Edge, len(edges))
	copy(sorted, edges)
	sort.Slice(sorted, func(i, j int) bool { return lessEdge(sorted[i], sorted[j]) })

	d := newDSU(n)
	inTree := make(map[int]bool, len(edges))
	var tree []Edge
	total := 0
	for _, e := range sorted {
		if e.U == e.V {
			continue // self-loops never belong to a forest
		}
		if d.union(e.U, e.V) {
			inTree[e.ID] = true
			tree = append(tree, e)
			total += e.W
		}
	}

	comps := 0
	for i := 0; i < n; i++ {
		if d.find(i) == i {
			comps++
		}
	}

	repl := computeReplacements(n, edges, tree, inTree)

	sort.Slice(tree, func(i, j int) bool { return tree[i].ID < tree[j].ID })
	return Analysis{
		Components:  comps,
		TotalWeight: total,
		TreeEdges:   tree,
		Replacement: repl,
	}
}

// computeReplacements considers EVERY non-tree edge: each one covers all tree
// edges on the unique tree path between its endpoints, and is a replacement
// candidate for exactly those edges. For each tree edge we keep the cheapest
// candidate by (weight, id). This is the standard path-covering argument, not
// a local guess along a single non-tree edge.
func computeReplacements(n int, edges []Edge, tree []Edge, inTree map[int]bool) map[int]*Edge {
	// Tree adjacency: neighbor + tree edge id.
	type nb struct {
		to     int
		edgeID int
	}
	adj := make([][]nb, n)
	for _, e := range tree {
		adj[e.U] = append(adj[e.U], nb{e.V, e.ID})
		adj[e.V] = append(adj[e.V], nb{e.U, e.ID})
	}

	best := make(map[int]*Edge, len(tree))
	for _, e := range tree {
		best[e.ID] = nil
	}

	consider := func(treeEdgeID int, cand Edge) {
		cur := best[treeEdgeID]
		if cur == nil || lessEdge(cand, *cur) {
			c := cand
			best[treeEdgeID] = &c
		}
	}

	for _, e := range edges {
		if inTree[e.ID] || e.U == e.V {
			continue
		}
		// BFS from e.U to e.V inside the tree, recording the tree edge
		// used to reach each node.
		prevNode := make([]int, n)
		prevEdge := make([]int, n)
		seen := make([]bool, n)
		queue := make([]int, 0, n)
		seen[e.U] = true
		queue = append(queue, e.U)
		for len(queue) > 0 && !seen[e.V] {
			x := queue[0]
			queue = queue[1:]
			for _, edge := range adj[x] {
				if !seen[edge.to] {
					seen[edge.to] = true
					prevNode[edge.to] = x
					prevEdge[edge.to] = edge.edgeID
					queue = append(queue, edge.to)
				}
			}
		}
		// Walk the path back from e.V to e.U, covering each tree edge.
		for x := e.V; x != e.U; x = prevNode[x] {
			consider(prevEdge[x], e)
		}
	}
	return best
}
