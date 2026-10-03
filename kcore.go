// Package kcore implements K-core decomposition (coreness / core-number
// computation) for large sparse undirected graphs, together with
// incremental maintenance of core numbers under edge insertions and
// deletions.
//
// Batch decomposition uses the classic O(V+E) bucket-based peeling
// algorithm of Batagelj & Zaversnik. Incremental updates rely on the
// well-known locality lemma (Sariyuce et al., PVLDB'13): inserting or
// deleting an edge (u,v) can only change the core number of vertices
// whose coreness equals K = min(core(u), core(v)), and only by one.
// The update therefore re-propagates coreness solely inside the local
// K-core component touching the modified edge, instead of recomputing
// the decomposition globally.
package kcore

import "sort"

// Graph is an undirected, unweighted graph with maintained core numbers.
type Graph struct {
	adj  map[int]map[int]struct{}
	core map[int]int

	// computed reports whether core currently reflects the adjacency.
	computed bool
	// lastWork counts vertices visited by the most recent maintenance
	// operation (full decomposition or incremental propagation). It is
	// a proxy for update cost.
	lastWork int
}

// New returns an empty graph.
func New() *Graph {
	return &Graph{
		adj:  make(map[int]map[int]struct{}),
		core: make(map[int]int),
	}
}

// AddNode inserts an isolated vertex if absent.
func (g *Graph) AddNode(id int) {
	if _, ok := g.adj[id]; ok {
		return
	}
	g.adj[id] = make(map[int]struct{})
	if g.computed {
		g.core[id] = 0
	}
}

// HasEdge reports whether edge (u,v) exists.
func (g *Graph) HasEdge(u, v int) bool {
	_, ok := g.adj[u][v]
	return ok
}

// AddEdge inserts edge (u,v). Self-loops and duplicate edges are
// ignored. If core numbers are current, only the affected local
// neighbourhood is re-propagated; otherwise the update is deferred to
// the next full decomposition.
func (g *Graph) AddEdge(u, v int) {
	if u == v {
		return
	}
	g.AddNode(u)
	g.AddNode(v)
	if g.HasEdge(u, v) {
		return
	}
	g.adj[u][v] = struct{}{}
	g.adj[v][u] = struct{}{}
	if g.computed {
		g.propagateInsert(u, v)
	}
}

// RemoveEdge deletes edge (u,v) if present, re-propagating coreness
// locally when core numbers are current.
func (g *Graph) RemoveEdge(u, v int) {
	if !g.HasEdge(u, v) {
		return
	}
	if g.computed {
		g.propagateDelete(u, v) // removes the edge itself
		return
	}
	delete(g.adj[u], v)
	delete(g.adj[v], u)
}

// NodeCount returns the number of vertices.
func (g *Graph) NodeCount() int { return len(g.adj) }

// EdgeCount returns the number of undirected edges.
func (g *Graph) EdgeCount() int {
	m := 0
	for _, nb := range g.adj {
		m += len(nb)
	}
	return m / 2
}

// LastUpdateWork returns how many vertices the last maintenance
// operation visited (full decomposition visits all n vertices).
func (g *Graph) LastUpdateWork() int { return g.lastWork }

// Decompose recomputes the core number of every vertex from scratch
// using the O(V+E) bucket-peeling algorithm and makes the maintained
// coreness current again.
func (g *Graph) Decompose() {
	n := len(g.adj)
	deg := make(map[int]int, n)
	maxDeg := 0
	for v, nb := range g.adj {
		deg[v] = len(nb)
		if deg[v] > maxDeg {
			maxDeg = deg[v]
		}
	}

	// Bucket-sort vertices by degree.
	bin := make([]int, maxDeg+1)
	for _, d := range deg {
		bin[d]++
	}
	start := 0
	for d := 0; d <= maxDeg; d++ {
		bin[d], start = start, start+bin[d]
	}
	vert := make([]int, n)
	pos := make(map[int]int, n)
	for v, d := range deg {
		pos[v] = bin[d]
		vert[bin[d]] = v
		bin[d]++
	}
	for d := maxDeg; d > 0; d-- {
		bin[d] = bin[d-1]
	}
	bin[0] = 0

	// Peel vertices in non-decreasing degree order.
	for i := 0; i < n; i++ {
		v := vert[i]
		g.core[v] = deg[v]
		for w := range g.adj[v] {
			if deg[w] > deg[v] {
				dw := deg[w]
				pw, pu := pos[w], bin[dw]
				u := vert[pu]
				vert[pw], vert[pu] = u, w
				pos[w], pos[u] = pu, pw
				bin[dw]++
				deg[w]--
			}
		}
	}
	g.computed = true
	g.lastWork = n
}

func (g *Graph) ensureComputed() {
	if !g.computed {
		g.Decompose()
	}
}

// CoreNumber returns the maintained coreness of vertex id
// (0 for unknown or isolated vertices).
func (g *Graph) CoreNumber(id int) int {
	g.ensureComputed()
	return g.core[id]
}

// CoreNumbers returns a copy of the full coreness map.
func (g *Graph) CoreNumbers() map[int]int {
	g.ensureComputed()
	out := make(map[int]int, len(g.core))
	for v, c := range g.core {
		out[v] = c
	}
	return out
}

// KCoreNodes returns the sorted vertex set of the k-core, i.e. all
// vertices whose core number is at least k.
func (g *Graph) KCoreNodes(k int) []int {
	g.ensureComputed()
	var out []int
	for v, c := range g.core {
		if c >= k {
			out = append(out, v)
		}
	}
	sort.Ints(out)
	return out
}

// Layers groups vertices by their exact core number, sorted per layer.
func (g *Graph) Layers() map[int][]int {
	g.ensureComputed()
	layers := make(map[int][]int)
	for v, c := range g.core {
		layers[c] = append(layers[c], v)
	}
	for _, vs := range layers {
		sort.Ints(vs)
	}
	return layers
}

// MaxCore returns the degeneracy (highest core number) of the graph.
func (g *Graph) MaxCore() int {
	g.ensureComputed()
	max := 0
	for _, c := range g.core {
		if c > max {
			max = c
		}
	}
	return max
}

// collectComponent returns the connected set of vertices with
// coreness == k reachable from the given seeds through vertices of
// coreness == k. Only seeds that themselves have coreness k contribute.
func (g *Graph) collectComponent(k int, seeds ...int) map[int]bool {
	cand := make(map[int]bool)
	var queue []int
	for _, s := range seeds {
		if g.core[s] == k && !cand[s] {
			cand[s] = true
			queue = append(queue, s)
		}
	}
	for len(queue) > 0 {
		x := queue[0]
		queue = queue[1:]
		for w := range g.adj[x] {
			if g.core[w] == k && !cand[w] {
				cand[w] = true
				queue = append(queue, w)
			}
		}
	}
	return cand
}

// propagateInsert incrementally raises coreness after edge (u,v) has
// been inserted. By the locality lemma only vertices with coreness
// K = min(core(u), core(v)) can move, and only up to K+1. Within the
// touched K-component we peel away every vertex that cannot reach
// K+1 supporters; the survivors are promoted.
func (g *Graph) propagateInsert(u, v int) {
	k := g.core[u]
	if g.core[v] < k {
		k = g.core[v]
	}
	cand := g.collectComponent(k, u, v)

	// support[x] = neighbours with coreness > k plus not-yet-evicted
	// candidates. A vertex can be promoted iff it keeps > k supporters.
	support := make(map[int]int, len(cand))
	var queue []int
	for x := range cand {
		s := 0
		for w := range g.adj[x] {
			if g.core[w] > k || cand[w] {
				s++
			}
		}
		support[x] = s
		if s <= k {
			queue = append(queue, x)
		}
	}
	for len(queue) > 0 {
		x := queue[0]
		queue = queue[1:]
		s, ok := support[x]
		if !ok || s > k {
			continue
		}
		delete(support, x) // evicted: cannot be promoted
		for w := range g.adj[x] {
			if !cand[w] {
				continue
			}
			if sw, ok := support[w]; ok {
				support[w] = sw - 1
				if sw-1 == k {
					queue = append(queue, w)
				}
			}
		}
	}
	for x := range support {
		g.core[x] = k + 1
	}
	g.lastWork = len(cand)
}

// propagateDelete incrementally lowers coreness around the removed
// edge (u,v). Only vertices with coreness K = min(core(u), core(v))
// can drop, and only to K-1. Inside the touched K-component we
// iteratively demote every vertex whose support (neighbours with
// coreness > k plus not-yet-demoted candidates) falls below k.
func (g *Graph) propagateDelete(u, v int) {
	k := g.core[u]
	if g.core[v] < k {
		k = g.core[v]
	}
	delete(g.adj[u], v)
	delete(g.adj[v], u)

	cand := g.collectComponent(k, u, v)
	support := make(map[int]int, len(cand))
	var queue []int
	for x := range cand {
		s := 0
		for w := range g.adj[x] {
			if g.core[w] > k || cand[w] {
				s++
			}
		}
		support[x] = s
		if s < k {
			queue = append(queue, x)
		}
	}
	for len(queue) > 0 {
		x := queue[0]
		queue = queue[1:]
		s, ok := support[x]
		if !ok || s >= k {
			continue
		}
		delete(support, x)
		g.core[x] = k - 1 // demoted
		for w := range g.adj[x] {
			if !cand[w] {
				continue
			}
			if sw, ok := support[w]; ok {
				support[w] = sw - 1
				if sw-1 == k-1 {
					queue = append(queue, w)
				}
			}
		}
	}
	g.lastWork = len(cand)
}
