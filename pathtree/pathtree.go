// Package pathtree maintains integer loads on the nodes of a tree and
// supports adding a (possibly negative) delta to every node on a path and
// querying the maximum load on a path together with the smallest-numbered
// node attaining it.
//
// Paths are defined on the undirected tree, so changing the logical root
// with SetRoot never affects the result of path operations.
//
// All tree traversals are iterative: the recursion depth of every operation
// is O(log n) (segment tree only), so arbitrarily long chains are safe.
package pathtree

import "fmt"

// Tree is a heavy-light decomposed tree with a lazy segment tree on top.
type Tree struct {
	n      int
	edges  [][2]int
	values []int64
	built  bool

	root int // logical root only; path semantics are undirected

	// CSR adjacency of the undirected tree.
	head []int
	adj  []int

	// Heavy-light decomposition, computed for the fixed decomposition
	// root 0. Iterative construction, no recursion proportional to n.
	parent []int
	depth  []int
	top    []int
	dfn    []int
	inv    []int

	seg *segTree
}

// New returns a tree with n nodes (numbered 0..n-1) and zero loads.
// Add edges with AddEdge, then call Build before any path operation.
func New(n int) *Tree {
	if n < 1 {
		panic("pathtree: New requires n >= 1")
	}
	return &Tree{n: n, values: make([]int64, n)}
}

// NewWithValues returns a tree whose node i starts with load values[i].
func NewWithValues(values []int64) *Tree {
	if len(values) < 1 {
		panic("pathtree: NewWithValues requires at least one node")
	}
	t := &Tree{n: len(values), values: make([]int64, len(values))}
	copy(t.values, values)
	return t
}

// AddEdge inserts the undirected edge (u, v). It must be called before Build.
func (t *Tree) AddEdge(u, v int) {
	if t.built {
		panic("pathtree: AddEdge after Build")
	}
	t.checkNode(u)
	t.checkNode(v)
	if u == v {
		panic("pathtree: self loop")
	}
	t.edges = append(t.edges, [2]int{u, v})
}

// Build finalizes the edge set and prepares path operations.
// It panics unless the edges form a single tree over all n nodes.
func (t *Tree) Build() {
	if t.built {
		panic("pathtree: Build called twice")
	}
	if len(t.edges) != t.n-1 {
		panic(fmt.Sprintf("pathtree: a tree on %d nodes needs %d edges, got %d",
			t.n, t.n-1, len(t.edges)))
	}

	// CSR adjacency.
	deg := make([]int, t.n)
	for _, e := range t.edges {
		deg[e[0]]++
		deg[e[1]]++
	}
	t.head = make([]int, t.n+1)
	for i := 0; i < t.n; i++ {
		t.head[i+1] = t.head[i] + deg[i]
	}
	t.adj = make([]int, 2*len(t.edges))
	cur := make([]int, t.n)
	copy(cur, t.head[:t.n])
	for _, e := range t.edges {
		t.adj[cur[e[0]]] = e[1]
		cur[e[0]]++
		t.adj[cur[e[1]]] = e[0]
		cur[e[1]]++
	}

	t.parent = make([]int, t.n)
	t.depth = make([]int, t.n)
	t.top = make([]int, t.n)
	t.dfn = make([]int, t.n)
	t.inv = make([]int, t.n)

	// Pass 1 (iterative DFS from decomposition root 0): parent, depth,
	// subtree sizes and heavy children.
	size := make([]int, t.n)
	heavy := make([]int, t.n)
	for i := range t.parent {
		t.parent[i] = -2 // unvisited marker
	}
	t.parent[0] = -1
	order := make([]int, 0, t.n)
	stack := []int{0}
	for len(stack) > 0 {
		u := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		order = append(order, u)
		for i := t.head[u]; i < t.head[u+1]; i++ {
			w := t.adj[i]
			if t.parent[w] == -2 {
				t.parent[w] = u
				t.depth[w] = t.depth[u] + 1
				stack = append(stack, w)
			}
		}
	}
	if len(order) != t.n {
		panic("pathtree: edges do not form a connected tree")
	}
	for i := len(order) - 1; i >= 0; i-- {
		u := order[i]
		size[u] = 1
		heavy[u] = -1
		best := 0
		for j := t.head[u]; j < t.head[u+1]; j++ {
			w := t.adj[j]
			if w == t.parent[u] {
				continue
			}
			size[u] += size[w]
			if size[w] > best {
				best = size[w]
				heavy[u] = w
			}
		}
	}

	// Pass 2 (iterative): decompose into heavy paths, assign dfn so that
	// every heavy path occupies a contiguous segment.
	timer := 0
	type seed struct{ u, tp int }
	seeds := []seed{{0, 0}}
	for len(seeds) > 0 {
		s := seeds[len(seeds)-1]
		seeds = seeds[:len(seeds)-1]
		for u, tp := s.u, s.tp; u != -1; u = heavy[u] {
			t.top[u] = tp
			t.dfn[u] = timer
			t.inv[timer] = u
			timer++
			for j := t.head[u]; j < t.head[u+1]; j++ {
				w := t.adj[j]
				if w != t.parent[u] && w != heavy[u] {
					seeds = append(seeds, seed{w, w})
				}
			}
		}
	}

	t.seg = newSegTree(t.inv, t.values)
	t.built = true
}

// SetRoot records r as the logical root. Path operations act on the
// undirected tree and are therefore identical for every root choice.
func (t *Tree) SetRoot(r int) {
	t.checkNode(r)
	t.root = r
}

// Root returns the logical root set by SetRoot (0 by default).
func (t *Tree) Root() int { return t.root }

// AddPath adds delta (which may be negative) to the load of every node on
// the undirected path between u and v, endpoints included.
func (t *Tree) AddPath(u, v int, delta int64) {
	t.mustBuilt()
	t.checkNode(u)
	t.checkNode(v)
	for t.top[u] != t.top[v] {
		if t.depth[t.top[u]] < t.depth[t.top[v]] {
			u, v = v, u
		}
		t.seg.add(1, 0, t.n-1, t.dfn[t.top[u]], t.dfn[u], delta)
		u = t.parent[t.top[u]]
	}
	if t.depth[u] > t.depth[v] {
		u, v = v, u
	}
	t.seg.add(1, 0, t.n-1, t.dfn[u], t.dfn[v], delta)
}

// QueryPath returns the maximum load on the undirected path between u and v
// (endpoints included) and the smallest-numbered node attaining that maximum.
func (t *Tree) QueryPath(u, v int) (max int64, node int) {
	t.mustBuilt()
	t.checkNode(u)
	t.checkNode(v)
	first := true
	consider := func(m int64, id int) {
		if first || m > max || (m == max && id < node) {
			max, node = m, id
			first = false
		}
	}
	for t.top[u] != t.top[v] {
		if t.depth[t.top[u]] < t.depth[t.top[v]] {
			u, v = v, u
		}
		m, id := t.seg.query(1, 0, t.n-1, t.dfn[t.top[u]], t.dfn[u])
		consider(m, id)
		u = t.parent[t.top[u]]
	}
	if t.depth[u] > t.depth[v] {
		u, v = v, u
	}
	m, id := t.seg.query(1, 0, t.n-1, t.dfn[u], t.dfn[v])
	consider(m, id)
	return max, node
}

func (t *Tree) mustBuilt() {
	if !t.built {
		panic("pathtree: call Build before path operations")
	}
}

func (t *Tree) checkNode(u int) {
	if u < 0 || u >= t.n {
		panic(fmt.Sprintf("pathtree: node %d out of range [0,%d)", u, t.n))
	}
}

// segTree is a lazy-propagation segment tree over the dfn order.
// Each segment keeps the maximum load and the smallest node number
// attaining it. Recursion depth is O(log n).
type segTree struct {
	max  []int64
	idx  []int
	lazy []int64
}

func newSegTree(inv []int, values []int64) *segTree {
	n := len(inv)
	s := &segTree{
		max:  make([]int64, 4*n),
		idx:  make([]int, 4*n),
		lazy: make([]int64, 4*n),
	}
	s.build(inv, values, 1, 0, n-1)
	return s
}

func (s *segTree) build(inv []int, values []int64, i, l, r int) {
	if l == r {
		s.max[i] = values[inv[l]]
		s.idx[i] = inv[l]
		return
	}
	m := (l + r) >> 1
	s.build(inv, values, i*2, l, m)
	s.build(inv, values, i*2+1, m+1, r)
	s.pull(i)
}

func (s *segTree) pull(i int) {
	l, r := i*2, i*2+1
	switch {
	case s.max[l] > s.max[r]:
		s.max[i], s.idx[i] = s.max[l], s.idx[l]
	case s.max[r] > s.max[l]:
		s.max[i], s.idx[i] = s.max[r], s.idx[r]
	case s.idx[l] < s.idx[r]:
		s.max[i], s.idx[i] = s.max[l], s.idx[l]
	default:
		s.max[i], s.idx[i] = s.max[r], s.idx[r]
	}
}

func (s *segTree) apply(i int, delta int64) {
	s.max[i] += delta
	s.lazy[i] += delta
}

func (s *segTree) push(i int) {
	if s.lazy[i] != 0 {
		s.apply(i*2, s.lazy[i])
		s.apply(i*2+1, s.lazy[i])
		s.lazy[i] = 0
	}
}

func (s *segTree) add(i, l, r, ql, qr int, delta int64) {
	if ql <= l && r <= qr {
		s.apply(i, delta)
		return
	}
	s.push(i)
	m := (l + r) >> 1
	if ql <= m {
		s.add(i*2, l, m, ql, qr, delta)
	}
	if qr > m {
		s.add(i*2+1, m+1, r, ql, qr, delta)
	}
	s.pull(i)
}

func (s *segTree) query(i, l, r, ql, qr int) (int64, int) {
	if ql <= l && r <= qr {
		return s.max[i], s.idx[i]
	}
	s.push(i)
	m := (l + r) >> 1
	if qr <= m {
		return s.query(i*2, l, m, ql, qr)
	}
	if ql > m {
		return s.query(i*2+1, m+1, r, ql, qr)
	}
	lm, li := s.query(i*2, l, m, ql, qr)
	rm, ri := s.query(i*2+1, m+1, r, ql, qr)
	switch {
	case lm > rm:
		return lm, li
	case rm > lm:
		return rm, ri
	case li < ri:
		return lm, li
	default:
		return rm, ri
	}
}
