// Package dynconn answers offline dynamic connectivity queries over an
// append/delete operation log.
//
// The whole operation log is known up front. Edge insertions are
// reference-counted: adding the same edge repeatedly only bumps its count,
// and the edge disconnects only on the deletion that drops the count back
// to zero. Each edge lifetime is expanded into a half-open time interval,
// the intervals are pushed onto a segment tree over time, and a single DFS
// with a rollback DSU answers every query. Parallel edges and self-loops
// are supported.
package dynconn

// OpKind identifies the type of a graph mutation.
type OpKind int

const (
	// OpAdd inserts one copy of edge (U, V). Repeated inserts of the same
	// edge increment its reference count.
	OpAdd OpKind = iota
	// OpDel removes one copy of edge (U, V), decrementing its reference
	// count. The edge stays alive until the count drops to zero.
	OpDel
)

// Op is a single edge mutation. Ops are applied in slice order; the graph
// state at time t (0 <= t <= len(ops)) is the result of applying ops[:t].
type Op struct {
	Kind OpKind
	U, V int
}

// Query asks whether U and V are connected in the graph at time At, i.e.
// after the first At operations have been applied. At may be any historical
// operation index in [0, len(ops)]; out-of-range values are clamped.
type Query struct {
	U, V int
	At   int
}

// Interval is the half-open lifetime [L, R) of one edge activation: the
// edge is present in the graph at every time t with L <= t < R.
type Interval struct {
	U, V int
	L, R int
}

type edgeKey struct{ a, b int }

func canon(u, v int) edgeKey {
	if u > v {
		u, v = v, u
	}
	return edgeKey{u, v}
}

// AliveIntervals expands the operation log into edge lifetimes using
// reference-count semantics: an OpAdd on an already-alive edge only bumps
// its count, and the edge disappears on the OpDel that drops the count to
// zero. An OpDel on an edge with a zero count is a no-op. Edges still alive
// after the last operation are alive through time len(ops).
func AliveIntervals(ops []Op) []Interval {
	count := make(map[edgeKey]int)
	start := make(map[edgeKey]int)
	var ivs []Interval
	for i, op := range ops {
		k := canon(op.U, op.V)
		switch op.Kind {
		case OpAdd:
			if count[k] == 0 {
				start[k] = i + 1
			}
			count[k]++
		case OpDel:
			if count[k] == 0 {
				continue
			}
			count[k]--
			if count[k] == 0 {
				ivs = append(ivs, Interval{U: k.a, V: k.b, L: start[k], R: i + 1})
				delete(start, k)
			}
		}
	}
	n := len(ops)
	for k, c := range count {
		if c > 0 {
			ivs = append(ivs, Interval{U: k.a, V: k.b, L: start[k], R: n + 1})
		}
	}
	return ivs
}

// dsuChange records one union so it can be undone.
type dsuChange struct {
	child         int
	parentOfChild int
	root          int
	sizeOfRoot    int
}

// rollbackDSU is a union-find with union-by-size and no path compression,
// so every union can be reverted in O(1).
type rollbackDSU struct {
	parent []int
	size   []int
	stack  []dsuChange
}

func newRollbackDSU(n int) *rollbackDSU {
	d := &rollbackDSU{parent: make([]int, n), size: make([]int, n)}
	for i := range d.parent {
		d.parent[i] = i
		d.size[i] = 1
	}
	return d
}

func (d *rollbackDSU) find(x int) int {
	for d.parent[x] != x {
		x = d.parent[x]
	}
	return x
}

func (d *rollbackDSU) union(x, y int) {
	x = d.find(x)
	y = d.find(y)
	if x == y {
		return // already united, or a self-loop
	}
	if d.size[x] > d.size[y] {
		x, y = y, x
	}
	d.stack = append(d.stack, dsuChange{child: x, parentOfChild: x, root: y, sizeOfRoot: d.size[y]})
	d.parent[x] = y
	d.size[y] += d.size[x]
}

func (d *rollbackDSU) snapshot() int { return len(d.stack) }

func (d *rollbackDSU) rollback(snap int) {
	for len(d.stack) > snap {
		c := d.stack[len(d.stack)-1]
		d.stack = d.stack[:len(d.stack)-1]
		d.parent[c.child] = c.parentOfChild
		d.size[c.root] = c.sizeOfRoot
	}
}

func (d *rollbackDSU) connected(x, y int) bool { return d.find(x) == d.find(y) }

// Answers resolves every query against the operation log. The result slice
// is aligned with queries: ans[i] reports whether queries[i].U and
// queries[i].V are connected at time queries[i].At.
//
// It runs in O((len(ops) + len(queries)) log len(ops) * alpha(V)) time.
func Answers(ops []Op, queries []Query) []bool {
	ans := make([]bool, len(queries))
	if len(queries) == 0 {
		return ans
	}
	n := len(ops)

	maxV := -1
	bump := func(x int) {
		if x > maxV {
			maxV = x
		}
	}
	for _, op := range ops {
		bump(op.U)
		bump(op.V)
	}
	for _, q := range queries {
		bump(q.U)
		bump(q.V)
	}
	d := newRollbackDSU(maxV + 1)

	byTime := make([][]int, n+1)
	for i, q := range queries {
		at := q.At
		if at < 0 {
			at = 0
		}
		if at > n {
			at = n
		}
		byTime[at] = append(byTime[at], i)
	}

	// Segment tree over time points [0, n]; padded to a power of two.
	size := 1
	for size < n+1 {
		size <<= 1
	}
	tree := make([][]Interval, 2*size)
	var add func(node, nl, nr, l, r int, iv Interval)
	add = func(node, nl, nr, l, r int, iv Interval) {
		if r <= nl || nr <= l {
			return
		}
		if l <= nl && nr <= r {
			tree[node] = append(tree[node], iv)
			return
		}
		mid := (nl + nr) / 2
		add(node*2, nl, mid, l, r, iv)
		add(node*2+1, mid, nr, l, r, iv)
	}
	for _, iv := range AliveIntervals(ops) {
		if iv.L < iv.R {
			add(1, 0, size, iv.L, iv.R, iv)
		}
	}

	var dfs func(node, nl, nr int)
	dfs = func(node, nl, nr int) {
		snap := d.snapshot()
		for _, iv := range tree[node] {
			d.union(iv.U, iv.V)
		}
		if nr-nl == 1 {
			if nl <= n {
				for _, qi := range byTime[nl] {
					q := queries[qi]
					ans[qi] = d.connected(q.U, q.V)
				}
			}
		} else {
			mid := (nl + nr) / 2
			dfs(node*2, nl, mid)
			dfs(node*2+1, mid, nr)
		}
		d.rollback(snap) // undo before returning so siblings see a clean state
	}
	dfs(1, 0, size)
	return ans
}
