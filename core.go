package kcore

import "sort"

// CoreNumbers computes the core number of every vertex with the
// Batagelj-Zaversnik algorithm in O(V+E) time, which is the standard
// choice for large sparse graphs. The core number of v is the largest k
// such that v belongs to the k-core of g.
func CoreNumbers(g *Graph) map[int]int {
	verts := g.Vertices()
	n := len(verts)
	core := make(map[int]int, n)
	if n == 0 {
		return core
	}

	deg := make(map[int]int, n)
	maxDeg := 0
	for _, v := range verts {
		d := g.Degree(v)
		deg[v] = d
		if d > maxDeg {
			maxDeg = d
		}
	}

	// Bucket-sort vertices by degree: binStart[d] is the first index of
	// the degree-d bucket inside vert.
	binStart := make([]int, maxDeg+1)
	for _, v := range verts {
		binStart[deg[v]]++
	}
	start := 0
	for d := 0; d <= maxDeg; d++ {
		num := binStart[d]
		binStart[d] = start
		start += num
	}
	vert := make([]int, n)
	pos := make(map[int]int, n)
	next := make([]int, maxDeg+1)
	copy(next, binStart)
	for _, v := range verts {
		pos[v] = next[deg[v]]
		vert[pos[v]] = v
		next[deg[v]]++
	}
	// md is the mutable "residual degree" used while peeling in
	// increasing-degree order.
	md := make(map[int]int, n)
	for v, d := range deg {
		md[v] = d
	}
	for i := 0; i < n; i++ {
		v := vert[i]
		core[v] = md[v]
		for w := range g.adj[v] {
			if md[w] > md[v] {
				// Move w to the front of its degree bucket and
				// decrement its residual degree.
				pw := pos[w]
				pu := binStart[md[w]]
				u := vert[pu]
				vert[pw], vert[pu] = u, w
				pos[w], pos[u] = pu, pw
				binStart[md[w]]++
				md[w]--
			}
		}
	}
	return core
}

// PeelKCore returns the vertex set of the k-core by dynamically removing
// vertices whose (residual) degree drops below k until convergence.
func PeelKCore(g *Graph, k int) map[int]bool {
	deg := make(map[int]int, g.NumVertices())
	removed := make(map[int]bool, g.NumVertices())
	var queue []int
	for _, v := range g.Vertices() {
		d := g.Degree(v)
		deg[v] = d
		if d < k {
			queue = append(queue, v)
		}
	}
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		if removed[v] {
			continue
		}
		removed[v] = true
		for w := range g.adj[v] {
			if removed[w] {
				continue
			}
			deg[w]--
			if deg[w] < k {
				queue = append(queue, w)
			}
		}
	}
	keep := make(map[int]bool, g.NumVertices()-len(removed))
	for _, v := range g.Vertices() {
		if !removed[v] {
			keep[v] = true
		}
	}
	return keep
}

// KCoreSubgraph returns the induced k-core subgraph of g.
func KCoreSubgraph(g *Graph, k int) *Graph {
	keep := PeelKCore(g, k)
	sub := NewGraph()
	for v := range keep {
		sub.AddVertex(v)
	}
	for v := range keep {
		for _, w := range g.Neighbors(v) {
			if w > v && keep[w] {
				sub.AddEdge(v, w)
			}
		}
	}
	return sub
}

// Layers groups vertices by exact core number: Layers(cores)[k] is the
// sorted list of vertices whose core number equals k (the "k-shell").
func Layers(cores map[int]int) map[int][]int {
	layers := make(map[int][]int)
	for v, c := range cores {
		layers[c] = append(layers[c], v)
	}
	for k := range layers {
		sort.Ints(layers[k])
	}
	return layers
}

// MaxCore returns the largest core number present (the degeneracy).
func MaxCore(cores map[int]int) int {
	max := 0
	for _, c := range cores {
		if c > max {
			max = c
		}
	}
	return max
}
