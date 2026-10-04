// Package treepath answers "number of distinct colors on the path between
// two nodes" queries on a tree, interleaved with single-node recolor
// updates. Colors may be introduced online (a recolor may use a color
// value never seen before).
//
// Algorithm: offline Mo's algorithm with modifications on the Euler tour
// of the tree (each node appears exactly twice), combined with binary
// lifting LCA. Every query is answered at the exact modification time it
// was issued, so later recolors never leak into earlier answers.
//
// Complexity (N nodes, Q queries, U updates, C distinct colors):
//   - Preprocessing: O(N log N) for LCA tables, O(N) Euler tour.
//   - Mo sweep: O((N + Q) * N^(2/3)) add/remove steps, each O(1) average,
//     using block size (2N)^(2/3). Total time O((N + Q) * N^(2/3) + N log N).
//   - Memory: O(N log N + Q + C).
//
// All results are deterministic: the Mo ordering is a total order with
// index tie-breaking, independent of map iteration order.
package treepath

import (
	"math"
	"math/bits"
	"sort"
)

// Edge is an undirected tree edge between nodes U and V (0-indexed).
type Edge struct{ U, V int }

// OpKind distinguishes queries from recolor updates.
type OpKind uint8

const (
	// OpQuery asks for the number of distinct colors on the path A<->B.
	OpQuery OpKind = iota
	// OpRecolor sets the color of node A to color B.
	OpRecolor
)

// Op is one operation in the offline stream. For OpQuery, A and B are the
// two endpoints. For OpRecolor, A is the node and B is its new color.
type Op struct {
	Kind OpKind
	A, B int
}

// Query builds a path-distinct-colors query for endpoints u and v.
func Query(u, v int) Op { return Op{Kind: OpQuery, A: u, B: v} }

// Recolor builds an update setting node's color to color.
func Recolor(node, color int) Op { return Op{Kind: OpRecolor, A: node, B: color} }

type recolor struct {
	node     int
	prev, nw int
}

type moQuery struct {
	l, r, t int
	idx     int
	extra   int // LCA node counted separately, -1 if none
}

// DistinctColorsOnPaths processes ops in order and returns one answer per
// OpQuery, in query order. Each query sees exactly the colors in effect at
// its position in the op stream (never final colors). The tree may be
// rooted arbitrarily; root only affects internal decomposition, never the
// answers.
func DistinctColorsOnPaths(n int, edges []Edge, initColors []int, ops []Op, root int) []int {
	adj := make([][]int, n)
	for _, e := range edges {
		adj[e.U] = append(adj[e.U], e.V)
		adj[e.V] = append(adj[e.V], e.U)
	}

	// Iterative DFS: Euler tour of length 2n, depth, binary lifting parents.
	log := max(1, bits.Len(uint(max(n, 1))))
	up := make([][]int, log)
	for k := range up {
		up[k] = make([]int, n)
	}
	tin := make([]int, n)
	tout := make([]int, n)
	depth := make([]int, n)
	euler := make([]int, 0, 2*n)

	type frame struct{ v, p, idx int }
	stack := []frame{{v: root, p: -1}}
	for len(stack) > 0 {
		f := &stack[len(stack)-1]
		if f.idx == 0 {
			tin[f.v] = len(euler)
			euler = append(euler, f.v)
			if f.p >= 0 {
				up[0][f.v] = f.p
			} else {
				up[0][f.v] = f.v
			}
		}
		if f.idx < len(adj[f.v]) {
			to := adj[f.v][f.idx]
			f.idx++
			if to == f.p {
				continue
			}
			depth[to] = depth[f.v] + 1
			stack = append(stack, frame{v: to, p: f.v})
		} else {
			tout[f.v] = len(euler)
			euler = append(euler, f.v)
			stack = stack[:len(stack)-1]
		}
	}
	for k := 1; k < log; k++ {
		for v := 0; v < n; v++ {
			up[k][v] = up[k-1][up[k-1][v]]
		}
	}
	lca := func(u, v int) int {
		if depth[u] < depth[v] {
			u, v = v, u
		}
		d := depth[u] - depth[v]
		for k := log - 1; k >= 0; k-- {
			if d>>k&1 == 1 {
				u = up[k][u]
			}
		}
		if u == v {
			return u
		}
		for k := log - 1; k >= 0; k-- {
			if up[k][u] != up[k][v] {
				u, v = up[k][u], up[k][v]
			}
		}
		return up[0][u]
	}

	// Split ops into updates (with prev/new colors) and Mo queries.
	var updates []recolor
	var qs []moQuery
	nQueries := 0
	nodeColor := append([]int(nil), initColors...)
	for _, op := range ops {
		if op.Kind == OpRecolor {
			updates = append(updates, recolor{node: op.A, prev: nodeColor[op.A], nw: op.B})
			nodeColor[op.A] = op.B
			continue
		}
		u, v := op.A, op.B
		if tin[u] > tin[v] {
			u, v = v, u
		}
		var q moQuery
		if w := lca(u, v); w == u {
			q = moQuery{l: tin[u], r: tin[v], t: len(updates), idx: nQueries, extra: -1}
		} else {
			q = moQuery{l: tout[u], r: tin[v], t: len(updates), idx: nQueries, extra: w}
		}
		qs = append(qs, q)
		nQueries++
	}

	// Deterministic Mo ordering with modifications: sort by (L block,
	// R block, time), alternating direction per block parity, index as
	// final tie-break so the order is total.
	m := max(len(euler), 1)
	block := max(1, int(math.Pow(float64(m), 2.0/3.0)))
	sort.Slice(qs, func(i, j int) bool {
		a, b := qs[i], qs[j]
		ba, bb := a.l/block, b.l/block
		if ba != bb {
			return ba < bb
		}
		ra, rb := a.r/block, b.r/block
		if ra != rb {
			if ba&1 == 0 {
				return ra < rb
			}
			return ra > rb
		}
		if a.t != b.t {
			if ra&1 == 0 {
				return a.t < b.t
			}
			return a.t > b.t
		}
		return a.idx < b.idx
	})

	// The update scan above left nodeColor at final colors; rewind it
	// to the initial coloring, since the sweep starts at time 0.
	nodeColor = append([]int(nil), initColors...)

	// Sweep state. Colors are compressed lazily, so brand-new colors can
	// appear in updates at any time.
	colorID := make(map[int]int)
	cnt := make([]int, 0, 16)
	getID := func(c int) int {
		if id, ok := colorID[c]; ok {
			return id
		}
		id := len(cnt)
		colorID[c] = id
		cnt = append(cnt, 0)
		return id
	}
	inPath := make([]bool, n)
	distinct := 0

	addColor := func(c int) {
		id := getID(c)
		if cnt[id] == 0 {
			distinct++
		}
		cnt[id]++
	}
	removeColor := func(c int) {
		id := getID(c)
		cnt[id]--
		if cnt[id] == 0 {
			distinct--
		}
	}
	toggle := func(pos int) {
		v := euler[pos]
		if inPath[v] {
			removeColor(nodeColor[v])
		} else {
			addColor(nodeColor[v])
		}
		inPath[v] = !inPath[v]
	}
	// moveTime applies (dir=+1) or reverts (dir=-1) update t.
	moveTime := func(t, dir int) {
		u := updates[t]
		oldC, newC := u.prev, u.nw
		if dir < 0 {
			oldC, newC = newC, oldC
		}
		if inPath[u.node] {
			removeColor(oldC)
			addColor(newC)
		}
		nodeColor[u.node] = newC
	}

	answers := make([]int, nQueries)
	curL, curR, curT := 0, -1, 0
	for _, q := range qs {
		for curL > q.l {
			curL--
			toggle(curL)
		}
		for curR < q.r {
			curR++
			toggle(curR)
		}
		for curL < q.l {
			toggle(curL)
			curL++
		}
		for curR > q.r {
			toggle(curR)
			curR--
		}
		for curT < q.t {
			moveTime(curT, +1)
			curT++
		}
		for curT > q.t {
			curT--
			moveTime(curT, -1)
		}
		if q.extra >= 0 {
			addColor(nodeColor[q.extra])
		}
		answers[q.idx] = distinct
		if q.extra >= 0 {
			removeColor(nodeColor[q.extra])
		}
	}
	return answers
}
