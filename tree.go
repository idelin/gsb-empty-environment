// Package pathtree provides a tree whose nodes carry integer loads and that
// supports path-add and path-max queries. Paths are undirected: the root
// chosen at construction time is only an internal decomposition detail and
// never affects query results.
package pathtree

const negInf = int64(-1) << 62

type segNode struct {
	max  int64
	idx  int // smallest node id attaining max in this segment
	lazy int64
}

// segTree is a lazy-propagation segment tree over the base array produced by
// the heavy-light decomposition. Recursion depth is O(log n).
type segTree struct {
	n    int
	tree []segNode
}

func better(maxA int64, idxA int, maxB int64, idxB int) (int64, int) {
	if maxA != maxB {
		if maxA > maxB {
			return maxA, idxA
		}
		return maxB, idxB
	}
	if idxA < idxB {
		return maxA, idxA
	}
	return maxB, idxB
}

func newSegTree(vals []int64, ids []int) *segTree {
	n := len(vals)
	t := &segTree{n: n, tree: make([]segNode, 4*n+8)}
	var build func(p, l, r int)
	build = func(p, l, r int) {
		if l == r {
			t.tree[p] = segNode{max: vals[l], idx: ids[l]}
			return
		}
		m := (l + r) >> 1
		build(p*2, l, m)
		build(p*2+1, m+1, r)
		t.tree[p].max, t.tree[p].idx = better(t.tree[p*2].max, t.tree[p*2].idx,
			t.tree[p*2+1].max, t.tree[p*2+1].idx)
	}
	if n > 0 {
		build(1, 0, n-1)
	}
	return t
}

func (t *segTree) apply(p int, d int64) {
	t.tree[p].max += d
	t.tree[p].lazy += d
}

func (t *segTree) push(p int) {
	if t.tree[p].lazy != 0 {
		d := t.tree[p].lazy
		t.apply(p*2, d)
		t.apply(p*2+1, d)
		t.tree[p].lazy = 0
	}
}

func (t *segTree) add(p, l, r, ql, qr int, d int64) {
	if ql <= l && r <= qr {
		t.apply(p, d)
		return
	}
	t.push(p)
	m := (l + r) >> 1
	if ql <= m {
		t.add(p*2, l, m, ql, qr, d)
	}
	if qr > m {
		t.add(p*2+1, m+1, r, ql, qr, d)
	}
	t.tree[p].max, t.tree[p].idx = better(t.tree[p*2].max, t.tree[p*2].idx,
		t.tree[p*2+1].max, t.tree[p*2+1].idx)
}

func (t *segTree) query(p, l, r, ql, qr int) (int64, int) {
	if ql <= l && r <= qr {
		return t.tree[p].max, t.tree[p].idx
	}
	t.push(p)
	m := (l + r) >> 1
	if qr <= m {
		return t.query(p*2, l, m, ql, qr)
	}
	if ql > m {
		return t.query(p*2+1, m+1, r, ql, qr)
	}
	maxL, idxL := t.query(p*2, l, m, ql, qr)
	maxR, idxR := t.query(p*2+1, m+1, r, ql, qr)
	return better(maxL, idxL, maxR, idxR)
}

// Tree is a weighted tree supporting path updates and path queries.
type Tree struct {
	n      int
	parent []int
	depth  []int
	heavy  []int
	head   []int
	pos    []int
	seg    *segTree
}

// NewTree builds a tree on nodes 0..n-1 from the given undirected edges.
// root may be any node; it only affects the internal decomposition, never
// the (undirected) paths. init holds initial node loads and may be nil,
// in which case all loads start at zero.
func NewTree(n int, edges [][2]int, root int, init []int64) *Tree {
	if n < 1 {
		panic("pathtree: empty tree")
	}
	adj := make([][]int, n)
	for _, e := range edges {
		u, v := e[0], e[1]
		adj[u] = append(adj[u], v)
		adj[v] = append(adj[v], u)
	}
	t := &Tree{
		n:      n,
		parent: make([]int, n),
		depth:  make([]int, n),
		heavy:  make([]int, n),
		head:   make([]int, n),
		pos:    make([]int, n),
	}
	for i := range t.heavy {
		t.heavy[i] = -1
	}

	// Iterative first pass: parent, depth, traversal order.
	order := make([]int, 0, n)
	visited := make([]bool, n)
	visited[root] = true
	t.parent[root] = -1
	order = append(order, root)
	type frame struct{ v, ci int }
	stack := []frame{{v: root}}
	for len(stack) > 0 {
		f := &stack[len(stack)-1]
		if f.ci < len(adj[f.v]) {
			to := adj[f.v][f.ci]
			f.ci++
			if !visited[to] {
				visited[to] = true
				t.parent[to] = f.v
				t.depth[to] = t.depth[f.v] + 1
				order = append(order, to)
				stack = append(stack, frame{v: to})
			}
		} else {
			stack = stack[:len(stack)-1]
		}
	}

	// Subtree sizes and heavy children, in reverse traversal order.
	size := make([]int, n)
	for i := len(order) - 1; i >= 0; i-- {
		v := order[i]
		size[v] = 1
		best := 0
		for _, to := range adj[v] {
			if to == t.parent[v] {
				continue
			}
			size[v] += size[to]
			if size[to] > best {
				best = size[to]
				t.heavy[v] = to
			}
		}
	}

	// Iterative decompose: assign heads and base-array positions.
	inv := make([]int, n)
	cur := 0
	type item struct{ v, h int }
	todo := []item{{v: root, h: root}}
	for len(todo) > 0 {
		it := todo[len(todo)-1]
		todo = todo[:len(todo)-1]
		for v := it.v; v != -1; v = t.heavy[v] {
			t.head[v] = it.h
			t.pos[v] = cur
			inv[cur] = v
			cur++
			for _, to := range adj[v] {
				if to != t.parent[v] && to != t.heavy[v] {
					todo = append(todo, item{v: to, h: to})
				}
			}
		}
	}

	vals := make([]int64, n)
	if init != nil {
		for i := 0; i < n; i++ {
			vals[i] = init[inv[i]]
		}
	}
	t.seg = newSegTree(vals, inv)
	return t
}

// PathAdd adds delta (which may be negative) to every node on the
// undirected path between u and v.
func (t *Tree) PathAdd(u, v int, delta int64) {
	for t.head[u] != t.head[v] {
		if t.depth[t.head[u]] < t.depth[t.head[v]] {
			u, v = v, u
		}
		t.seg.add(1, 0, t.n-1, t.pos[t.head[u]], t.pos[u], delta)
		u = t.parent[t.head[u]]
	}
	if t.depth[u] > t.depth[v] {
		u, v = v, u
	}
	t.seg.add(1, 0, t.n-1, t.pos[u], t.pos[v], delta)
}

// PathMax returns the maximum load on the undirected path between u and v,
// together with the smallest-numbered node attaining that maximum.
func (t *Tree) PathMax(u, v int) (maxVal int64, node int) {
	maxVal, node = negInf, -1
	absorb := func(m int64, i int) {
		maxVal, node = better(maxVal, node, m, i)
	}
	for t.head[u] != t.head[v] {
		if t.depth[t.head[u]] < t.depth[t.head[v]] {
			u, v = v, u
		}
		m, i := t.seg.query(1, 0, t.n-1, t.pos[t.head[u]], t.pos[u])
		absorb(m, i)
		u = t.parent[t.head[u]]
	}
	if t.depth[u] > t.depth[v] {
		u, v = v, u
	}
	m, i := t.seg.query(1, 0, t.n-1, t.pos[u], t.pos[v])
	absorb(m, i)
	return maxVal, node
}
