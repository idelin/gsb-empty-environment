package matching

import "sort"

// EssentialEdges returns the IDs of edges that appear in every maximum
// matching, sorted ascending. If a vertex pair is essential, the IDs of
// all input edges with that pair are returned. It returns nil when no
// edge is forced, e.g. for a graph with no edges.
//
// It panics if any endpoint is outside [0, nLeft) x [0, nRight).
func EssentialEdges(nLeft, nRight int, edges []Edge) []int {
	g := newGraph(nLeft, nRight, edges)
	matchL, matchR := hopcroftKarp(g)

	// Alternating digraph on nLeft+nRight vertices: unmatched edges are
	// directed left -> right, matched edges right -> left. A matched edge
	// (u, v) is avoidable (some maximum matching omits it) iff it lies on
	// an alternating cycle, or u is reachable from a free left vertex, or
	// v can reach a free right vertex. Otherwise it is essential.
	n := nLeft + nRight
	fwd := make([][]int, n)
	rev := make([][]int, n)
	add := func(a, b int) {
		fwd[a] = append(fwd[a], b)
		rev[b] = append(rev[b], a)
	}
	for l := 0; l < nLeft; l++ {
		for _, r := range g.adj[l] {
			if matchL[l] == r {
				add(nLeft+r, l)
			} else {
				add(l, nLeft+r)
			}
		}
	}

	comp := tarjanSCC(fwd)

	var freeL, freeR []int
	for l := 0; l < nLeft; l++ {
		if matchL[l] == -1 {
			freeL = append(freeL, l)
		}
	}
	for r := 0; r < nRight; r++ {
		if matchR[r] == -1 {
			freeR = append(freeR, nLeft+r)
		}
	}
	fromFreeL := reachable(fwd, freeL)
	toFreeR := reachable(rev, freeR)

	essential := make(map[pair]bool)
	for l := 0; l < nLeft; l++ {
		r := matchL[l]
		if r < 0 {
			continue
		}
		rv := nLeft + r
		if comp[l] == comp[rv] || fromFreeL[l] || toFreeR[rv] {
			continue
		}
		essential[pair{l, r}] = true
	}

	var ids []int
	for _, e := range edges {
		if essential[pair{e.Left, e.Right}] {
			ids = append(ids, e.ID)
		}
	}
	sort.Ints(ids)
	return ids
}

// tarjanSCC returns the strongly connected component id of each vertex.
func tarjanSCC(adj [][]int) []int {
	n := len(adj)
	index := make([]int, n)
	low := make([]int, n)
	onStack := make([]bool, n)
	comp := make([]int, n)
	for i := range index {
		index[i] = -1
	}
	var stack []int
	counter, ncomp := 0, 0

	var visit func(v int)
	visit = func(v int) {
		index[v], low[v] = counter, counter
		counter++
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range adj[v] {
			if index[w] == -1 {
				visit(w)
				if low[w] < low[v] {
					low[v] = low[w]
				}
			} else if onStack[w] && index[w] < low[v] {
				low[v] = index[w]
			}
		}
		if low[v] == index[v] {
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				comp[w] = ncomp
				if w == v {
					break
				}
			}
			ncomp++
		}
	}
	for v := 0; v < n; v++ {
		if index[v] == -1 {
			visit(v)
		}
	}
	return comp
}

// reachable reports the vertices reachable from any of the sources.
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
