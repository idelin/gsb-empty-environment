// Package treecolor answers dynamic "distinct colors on tree path" queries.
//
// Supported operations (offline, all operations known up front):
//   - point recolor: change the color of a single node; new color values may
//     appear at any time (colors are "added online");
//   - path query: number of distinct colors on the simple path between two
//     nodes, evaluated against the color state at that moment in the
//     operation sequence (never against the final colors).
//
// Algorithm: Euler-tour flattening (each node appears exactly twice) +
// binary-lifting LCA + Mo's algorithm with modifications (time dimension).
//
// Complexity (n nodes, q queries, u updates, m = 2n tour length):
//   - preprocessing: O(n log n) for LCA, O(n) for the Euler tour;
//   - total: O((m + q) * m^(2/3)) with block size m^(2/3), i.e. O(n^(5/3))
//     when q, u = O(n); each query/update costs O(m^(2/3)) amortized;
//   - memory: O(n log n + q + C) where C is the number of distinct colors.
//
// Results are independent of the chosen root (the path of an unrooted tree
// is root-invariant) and output is deterministic: queries are answered in
// input order and no map iteration affects the result.
package treecolor

import "sort"

// OpKind distinguishes recolor updates from path queries.
type OpKind int

const (
	// OpUpdate recolors Node to Color.
	OpUpdate OpKind = iota
	// OpQuery asks for the number of distinct colors on path U--V.
	OpQuery
)

// Op is a single operation in the offline sequence. Update operations are
// numbered 0,1,2,... in their order of appearance; a query is evaluated
// after exactly the updates that precede it in the sequence.
type Op struct {
	Kind  OpKind
	Node  int   // OpUpdate: node to recolor, 0-based
	Color int64 // OpUpdate: new color (any value, may be brand new)
	U, V  int   // OpQuery: path endpoints, 0-based
}

// Solve processes the whole operation sequence and returns one answer per
// OpQuery, in the order the queries appear. root only selects the DFS root
// used internally; answers do not depend on it.
func Solve(n int, edges [][2]int, init []int64, ops []Op, root int) []int {
	adj := make([][]int, n)
	for _, e := range edges {
		adj[e[0]] = append(adj[e[0]], e[1])
		adj[e[1]] = append(adj[e[1]], e[0])
	}

	// Coordinate-compress every color that can ever appear.
	comp := map[int64]int{}
	for _, c := range init {
		comp[c] = 0
	}
	for _, op := range ops {
		if op.Kind == OpUpdate {
			comp[op.Color] = 0
		}
	}
	palette := make([]int64, 0, len(comp))
	for c := range comp {
		palette = append(palette, c)
	}
	sort.Slice(palette, func(i, j int) bool { return palette[i] < palette[j] })
	for i, c := range palette {
		comp[c] = i
	}

	cur := make([]int, n) // current compressed color per node
	for i, c := range init {
		cur[i] = comp[c]
	}

	// Iterative DFS: Euler tour of length 2n, tin/tout, depth, parent.
	const LOG = 20 // enough for n <= ~1e6/2 levels? grow dynamically below
	log := 1
	for (1 << log) < n {
		log++
	}
	up := make([][]int, log)
	for i := range up {
		up[i] = make([]int, n)
	}
	tin := make([]int, n)
	tout := make([]int, n)
	depth := make([]int, n)
	euler := make([]int, 0, 2*n)

	type frame struct{ v, next int }
	stack := []frame{{root, 0}}
	up[0][root] = root
	visited := make([]bool, n)
	visited[root] = true
	tin[root] = len(euler)
	euler = append(euler, root)
	for len(stack) > 0 {
		f := &stack[len(stack)-1]
		if f.next < len(adj[f.v]) {
			to := adj[f.v][f.next]
			f.next++
			if visited[to] {
				continue
			}
			visited[to] = true
			depth[to] = depth[f.v] + 1
			up[0][to] = f.v
			tin[to] = len(euler)
			euler = append(euler, to)
			stack = append(stack, frame{to, 0})
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

	lca := func(a, b int) int {
		if depth[a] < depth[b] {
			a, b = b, a
		}
		d := depth[a] - depth[b]
		for k := log - 1; k >= 0; k-- {
			if d>>k&1 == 1 {
				a = up[k][a]
			}
		}
		if a == b {
			return a
		}
		for k := log - 1; k >= 0; k-- {
			if up[k][a] != up[k][b] {
				a = up[k][a]
				b = up[k][b]
			}
		}
		return up[0][a]
	}

	// Collect updates and queries.
	type update struct{ node, color int }
	updates := []update{}
	type query struct{ l, r, t, extra, idx int }
	queries := []query{}
	qidx := 0
	for _, op := range ops {
		if op.Kind == OpUpdate {
			updates = append(updates, update{op.Node, comp[op.Color]})
		} else {
			u, v := op.U, op.V
			if tin[u] > tin[v] {
				u, v = v, u
			}
			var l, r, extra int
			if p := lca(u, v); p == u {
				l, r, extra = tin[u], tin[v], -1
			} else {
				l, r, extra = tout[u], tin[v], p
			}
			queries = append(queries, query{l, r, len(updates), extra, qidx})
			qidx++
		}
	}

	// Mo's algorithm with modifications, block size m^(2/3).
	m := len(euler)
	block := 1
	for block*block*block < m*m {
		block++
	}
	sort.Slice(queries, func(i, j int) bool {
		a, b := queries[i], queries[j]
		ba, bb := a.l/block, b.l/block
		if ba != bb {
			return ba < bb
		}
		ra, rb := a.r/block, b.r/block
		if ra != rb {
			if ba%2 == 0 {
				return ra < rb
			}
			return ra > rb
		}
		if ra%2 == 0 {
			if a.t != b.t {
				return a.t < b.t
			}
		} else if a.t != b.t {
			return a.t > b.t
		}
		return a.idx < b.idx
	})

	on := make([]bool, n)
	cnt := make([]int, len(palette))
	distinct := 0
	toggle := func(pos int) {
		v := euler[pos]
		c := cur[v]
		if on[v] {
			cnt[c]--
			if cnt[c] == 0 {
				distinct--
			}
		} else {
			if cnt[c] == 0 {
				distinct++
			}
			cnt[c]++
		}
		on[v] = !on[v]
	}
	apply := func(k int) { // fast-forward update k
		u := updates[k]
		v := u.node
		if on[v] {
			cnt[cur[v]]--
			if cnt[cur[v]] == 0 {
				distinct--
			}
			if cnt[u.color] == 0 {
				distinct++
			}
			cnt[u.color]++
		}
		cur[v] = u.color
	}
	// prevColors[k][v] history is implicit: to undo update k we need the color
	// before it, so precompute for each update the color it overwrites.
	// overwritten[k] = compressed color replaced by update k.
	overwritten := make([]int, len(updates))
	{
		tmp := make([]int, n)
		copy(tmp, cur)
		for k, u := range updates {
			overwritten[k] = tmp[u.node]
			tmp[u.node] = u.color
		}
	}
	undo := func(k int) {
		u := updates[k]
		v := u.node
		if on[v] {
			cnt[cur[v]]--
			if cnt[cur[v]] == 0 {
				distinct--
			}
			if cnt[overwritten[k]] == 0 {
				distinct++
			}
			cnt[overwritten[k]]++
		}
		cur[v] = overwritten[k]
	}

	answers := make([]int, qidx)
	curL, curR, curT := 0, -1, 0
	for _, q := range queries {
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
			apply(curT)
			curT++
		}
		for curT > q.t {
			curT--
			undo(curT)
		}
		ans := distinct
		if q.extra >= 0 { // LCA node outside the [l,r] window
			if cnt[cur[q.extra]] == 0 {
				ans++
			}
		}
		answers[q.idx] = ans
	}
	return answers
}
