// Package dynconn answers offline dynamic connectivity queries.
//
// The input is a sequence of edge additions and removals. Adding the same
// edge multiple times increments a reference count: the edge stays alive
// until it has been removed as many times as it was added. Parallel edges
// and self-loops are supported.
//
// Each query asks whether two vertices are connected in the graph as it
// was right after an arbitrary historical operation index.
//
// The solver expands every edge reference into a lifetime interval
// [addTime, removeTime), places the intervals on a segment tree over
// time, and walks the tree with a rollback DSU. State is rolled back
// exactly when leaving a segment tree node, so no connectivity leaks
// into sibling time segments.
package dynconn

import "fmt"

// OpKind is the kind of a graph operation.
type OpKind int

const (
	// AddEdge adds one reference to the edge (U, V).
	AddEdge OpKind = iota
	// RemoveEdge removes one reference to the edge (U, V).
	RemoveEdge
)

// Op is a single edge update. Operations are implicitly numbered 1..N
// by their position in the input slice.
type Op struct {
	Kind OpKind
	U, V int
}

// Query asks whether U and V are connected after the first Time
// operations have been applied. Time may be any value in [0, len(ops)],
// so any historical operation index can be inspected.
type Query struct {
	Time int
	U, V int
}

// edge is a canonicalized undirected edge.
type edge struct{ u, v int }

func canonical(u, v int) edge {
	if u > v {
		u, v = v, u
	}
	return edge{u, v}
}

// dsuChange records one union so it can be undone.
type dsuChange struct {
	child  int // root that was attached
	parent int // root it was attached to
	size   int // size[parent] before the merge
}

// rollbackDSU is a union-find with union by size and rollback support.
// It deliberately avoids path compression, which cannot be rolled back.
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
	x, y = d.find(x), d.find(y)
	if x == y {
		return
	}
	if d.size[x] > d.size[y] {
		x, y = y, x
	}
	d.stack = append(d.stack, dsuChange{child: x, parent: y, size: d.size[y]})
	d.parent[x] = y
	d.size[y] += d.size[x]
}

func (d *rollbackDSU) connected(x, y int) bool {
	return d.find(x) == d.find(y)
}

// checkpoint returns a token the structure can later be rolled back to.
func (d *rollbackDSU) checkpoint() int { return len(d.stack) }

// rollback undoes every union recorded after the checkpoint was taken.
func (d *rollbackDSU) rollback(to int) {
	for len(d.stack) > to {
		c := d.stack[len(d.stack)-1]
		d.stack = d.stack[:len(d.stack)-1]
		d.parent[c.child] = c.child
		d.size[c.parent] = c.size
	}
}

// interval is the lifetime [l, r) of one edge reference, in op indices.
type interval struct {
	l, r int
	e    edge
}

// lifetimes pairs every removal with the most recent unmatched addition
// of the same edge (LIFO). Because an edge is present whenever at least
// one of its references is alive, these intervals exactly reproduce the
// reference-counting semantics, including parallel edges.
func lifetimes(ops []Op) ([]interval, error) {
	open := make(map[edge][]int)
	var ivs []interval
	for i, op := range ops {
		t := i + 1
		e := canonical(op.U, op.V)
		switch op.Kind {
		case AddEdge:
			open[e] = append(open[e], t)
		case RemoveEdge:
			st := open[e]
			if len(st) == 0 {
				return nil, fmt.Errorf("dynconn: op %d removes edge (%d,%d) with no matching add", t, op.U, op.V)
			}
			ivs = append(ivs, interval{l: st[len(st)-1], r: t, e: e})
			open[e] = st[:len(st)-1]
		default:
			return nil, fmt.Errorf("dynconn: op %d has unknown kind %d", t, op.Kind)
		}
	}
	end := len(ops) + 1
	for e, st := range open {
		for _, l := range st {
			ivs = append(ivs, interval{l: l, r: end, e: e})
		}
	}
	return ivs, nil
}

// segTree stores edge intervals over the time range [0, n).
type segTree struct {
	n       int
	buckets [][]edge
}

func newSegTree(n int) *segTree {
	if n < 1 {
		n = 1
	}
	return &segTree{n: n, buckets: make([][]edge, 4*n)}
}

func (s *segTree) add(l, r int, e edge) {
	if l < r {
		s.addRec(1, 0, s.n, l, r, e)
	}
}

func (s *segTree) addRec(node, nl, nr, l, r int, e edge) {
	if r <= nl || nr <= l {
		return
	}
	if l <= nl && nr <= r {
		s.buckets[node] = append(s.buckets[node], e)
		return
	}
	mid := nl + (nr-nl)/2
	s.addRec(node*2, nl, mid, l, r, e)
	s.addRec(node*2+1, mid, nr, l, r, e)
}

func validate(numVertices int, ops []Op, queries []Query) error {
	if numVertices < 0 {
		return fmt.Errorf("dynconn: negative vertex count %d", numVertices)
	}
	inRange := func(v int) bool { return 0 <= v && v < numVertices }
	for i, op := range ops {
		if !inRange(op.U) || !inRange(op.V) {
			return fmt.Errorf("dynconn: op %d references vertex out of range [0,%d)", i+1, numVertices)
		}
	}
	for i, q := range queries {
		if q.Time < 0 || q.Time > len(ops) {
			return fmt.Errorf("dynconn: query %d has time %d outside [0,%d]", i, q.Time, len(ops))
		}
		if !inRange(q.U) || !inRange(q.V) {
			return fmt.Errorf("dynconn: query %d references vertex out of range [0,%d)", i, numVertices)
		}
	}
	return nil
}

// Solve answers every query against the graph state after ops[:Time].
//
// It runs in O((len(ops) + len(queries)) log len(ops) α(numVertices)).
func Solve(numVertices int, ops []Op, queries []Query) ([]bool, error) {
	if err := validate(numVertices, ops, queries); err != nil {
		return nil, err
	}
	ivs, err := lifetimes(ops)
	if err != nil {
		return nil, err
	}

	// Leaf t of the segment tree is the graph after t operations.
	tree := newSegTree(len(ops) + 1)
	for _, iv := range ivs {
		tree.add(iv.l, iv.r, iv.e)
	}

	byTime := make([][]int, len(ops)+1)
	for qi, q := range queries {
		byTime[q.Time] = append(byTime[q.Time], qi)
	}

	ans := make([]bool, len(queries))
	d := newRollbackDSU(numVertices)
	var walk func(node, nl, nr int)
	walk = func(node, nl, nr int) {
		cp := d.checkpoint()
		for _, e := range tree.buckets[node] {
			d.union(e.u, e.v)
		}
		if nl+1 == nr {
			for _, qi := range byTime[nl] {
				q := queries[qi]
				ans[qi] = d.connected(q.U, q.V)
			}
		} else {
			mid := nl + (nr-nl)/2
			walk(node*2, nl, mid)
			walk(node*2+1, mid, nr)
		}
		// Undo exactly what this node added so sibling subtrees,
		// which cover disjoint time ranges, never see this state.
		d.rollback(cp)
	}
	walk(1, 0, tree.n)
	return ans, nil
}
