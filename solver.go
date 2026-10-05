// Package treemedian solves the capacitated-free k-median (facility
// location) problem on a tree with non-negative edge lengths.
//
// Problem: given a tree with n nodes, integer demands d[v] >= 0, a set of
// forbidden nodes, and a budget of at most K facilities, choose facility
// locations (only on allowed nodes) minimizing the sum of
// d[v] * dist(v, nearest facility). The solver uses an exact tree dynamic
// program; if the operation budget is exhausted it falls back to a greedy
// feasible solution which is a valid upper bound on the optimum.
package treemedian

import "sort"

// Inf is a sentinel for impossible states; large enough to avoid overflow.
const Inf = int64(1) << 60

// DefaultMaxOps is the default operation budget for the exact DP.
const DefaultMaxOps = int64(200000000)

// Limits for the exact DP (choice tables pack child index and facility
// count into one int32).
const (
	maxDPNodes = 1 << 20
	maxDPK     = 1 << 12
)

// Edge is a weighted adjacency entry.
type Edge struct {
	To int
	W  int64
}

// Result is the solver output.
type Result struct {
	Feasible   bool  // false when positive demand exists but no facility may be placed
	Exact      bool  // false when the budget was exhausted: Cost is a feasible upper bound
	Cost       int64 // total weighted distance (upper bound when !Exact)
	Facilities []int // chosen facility node ids, sorted
	Assignment []int // Assignment[v] = facility serving v, -1 when no facility exists
}

// Solver holds the problem instance and DP workspace.
type Solver struct {
	N         int
	K         int
	Adj       [][]Edge
	Demand    []int64
	Forbidden []bool
	MaxOps    int64 // 0 => DefaultMaxOps

	ops       int64
	exhausted bool

	root     int
	parent   []int
	edgeW    []int64
	children [][]int
	order    []int // reverse DFS pre-order: children before parents
	sub      [][]int
	dist     [][]int64

	f      [][]int64   // f[u][k*N+a]: min cost of subtree(u), k facilities inside, nearest facility to u is a
	src    [][]byte    // src[u][k*N+a] == 1 => u itself hosts a facility
	choice [][][]int32 // choice[u][childIdx][k*N+a] packed: nearestB | kChild<<20
}

// NewSolver creates an instance with n nodes and facility budget k.
func NewSolver(n, k int) *Solver {
	if k < 0 {
		k = 0
	}
	return &Solver{
		N:         n,
		K:         k,
		Adj:       make([][]Edge, n),
		Demand:    make([]int64, n),
		Forbidden: make([]bool, n),
	}
}

// AddEdge adds an undirected edge with non-negative length w.
func (s *Solver) AddEdge(u, v int, w int64) {
	s.Adj[u] = append(s.Adj[u], Edge{To: v, W: w})
	s.Adj[v] = append(s.Adj[v], Edge{To: u, W: w})
}

// Solve computes the optimal (or, on budget exhaustion, a feasible greedy)
// facility placement.
func (s *Solver) Solve() Result {
	if s.MaxOps <= 0 {
		s.MaxOps = DefaultMaxOps
	}
	n := s.N
	if n == 0 {
		return Result{Feasible: true, Exact: true, Assignment: []int{}}
	}
	s.root = 0
	s.buildTree()
	s.computeDist()

	anyAllowed := false
	for i := 0; i < n; i++ {
		if !s.Forbidden[i] {
			anyAllowed = true
			break
		}
	}
	allZero := true
	for _, d := range s.Demand {
		if d != 0 {
			allZero = false
			break
		}
	}
	// Cases where the empty placement is forced or already optimal.
	if s.K == 0 || !anyAllowed || allZero {
		return s.buildResult(nil, true)
	}

	if !s.runDP() {
		// Budget exhausted (or instance too large for the exact DP):
		// return a feasible greedy solution, i.e. an upper bound.
		return s.greedy()
	}

	fr := s.f[s.root]
	best := Inf
	bk, ba := -1, -1
	for k := 1; k <= s.K; k++ {
		for a := 0; a < n; a++ {
			if v := fr[k*n+a]; v < best {
				best, bk, ba = v, k, a
			}
		}
	}
	if best == Inf {
		return s.buildResult(nil, true)
	}
	var facs []int
	s.reconstruct(s.root, bk, ba, &facs)
	sort.Ints(facs)
	return s.buildResult(facs, true)
}

// buildTree roots the tree at node 0 and prepares post-order and subtree lists.
func (s *Solver) buildTree() {
	n := s.N
	s.parent = make([]int, n)
	s.edgeW = make([]int64, n)
	s.children = make([][]int, n)
	visited := make([]bool, n)
	visited[s.root] = true
	s.parent[s.root] = -1
	stack := []int{s.root}
	var pre []int
	for len(stack) > 0 {
		u := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		pre = append(pre, u)
		for _, e := range s.Adj[u] {
			if !visited[e.To] {
				visited[e.To] = true
				s.parent[e.To] = u
				s.edgeW[e.To] = e.W
				s.children[u] = append(s.children[u], e.To)
				stack = append(stack, e.To)
			}
		}
	}
	// Reverse pre-order: every node precedes its parent.
	s.order = make([]int, 0, n)
	for i := len(pre) - 1; i >= 0; i-- {
		s.order = append(s.order, pre[i])
	}
	s.sub = make([][]int, n)
	for _, u := range s.order {
		su := []int{u}
		for _, c := range s.children[u] {
			su = append(su, s.sub[c]...)
		}
		s.sub[u] = su
	}
}

// computeDist computes all-pairs distances with one DFS per source: O(n^2).
func (s *Solver) computeDist() {
	n := s.N
	s.dist = make([][]int64, n)
	for src := 0; src < n; src++ {
		d := make([]int64, n)
		for i := range d {
			d[i] = -1
		}
		d[src] = 0
		stack := []int{src}
		for len(stack) > 0 {
			u := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for _, e := range s.Adj[u] {
				if d[e.To] < 0 {
					d[e.To] = d[u] + e.W
					stack = append(stack, e.To)
				}
			}
		}
		s.dist[src] = d
	}
}

// runDP executes the exact dynamic program. Returns false on budget
// exhaustion or when instance limits for the DP are exceeded.
func (s *Solver) runDP() bool {
	n, K := s.N, s.K
	if n >= maxDPNodes || K >= maxDPK {
		return false
	}
	// Rough work estimate; skip the DP (and its memory) when it cannot finish.
	est := int64(n) * int64(n) * int64(K+1) * int64(K+1) / 2
	if est > s.MaxOps {
		s.exhausted = true
		return false
	}
	s.f = make([][]int64, n)
	s.src = make([][]byte, n)
	s.choice = make([][][]int32, n)
	for _, u := range s.order {
		s.computeNode(u)
		if s.exhausted {
			return false
		}
	}
	return true
}

// computeNode fills f[u], src[u] and choice[u] from already-computed children.
func (s *Solver) computeNode(u int) {
	n, K := s.N, s.K
	size := (K + 1) * n
	cur := make([]int64, size)
	for i := range cur {
		cur[i] = Inf
	}
	cursrc := make([]byte, size)
	// Base case A: no facility at u. The nearest facility a must lie outside
	// the (so far empty) processed subtree, i.e. a != u.
	du := s.dist[u]
	dem := s.Demand[u]
	for a := 0; a < n; a++ {
		if a != u {
			cur[a] = dem * du[a] // k = 0 row
		}
	}
	// Base case B: facility at u (k = 1, nearest facility is u itself).
	if !s.Forbidden[u] {
		idx := n + u
		cur[idx] = 0
		cursrc[idx] = 1
	}
	s.choice[u] = make([][]int32, len(s.children[u]))
	for ci, c := range s.children[u] {
		nxt, nxtsrc, ch := s.merge(u, c, cur, cursrc)
		if s.exhausted {
			return
		}
		cur, cursrc = nxt, nxtsrc
		s.choice[u][ci] = ch
	}
	s.f[u] = cur
	s.src[u] = cursrc
}

// merge folds child c (already solved) into the partial table of u.
//
// Invariant for the tables: T[k][a] = min cost of the processed part of
// subtree(u) with k facilities inside, given the nearest facility to u
// (anywhere in the tree) is node a. For the child, its nearest facility b
// either lies inside subtree(c) with dist(c,b) <= dist(c,a), or equals a
// itself (only possible when a is outside subtree(c)).
func (s *Solver) merge(u, c int, cur []int64, cursrc []byte) ([]int64, []byte, []int32) {
	n, K := s.N, s.K
	fc := s.f[c]
	dc := s.dist[c]

	nodes := make([]int, len(s.sub[c]))
	copy(nodes, s.sub[c])
	sort.Slice(nodes, func(i, j int) bool {
		di, dj := dc[nodes[i]], dc[nodes[j]]
		if di != dj {
			return di < dj
		}
		return nodes[i] < nodes[j]
	})
	m := len(nodes)
	sd := make([]int64, m)
	inSub := make([]bool, n)
	for i, b := range nodes {
		sd[i] = dc[b]
		inSub[b] = true
	}

	// prefMin[k2][i] = min over the i closest subtree nodes b of fc[k2][b].
	prefMin := make([][]int64, K+1)
	prefArg := make([][]int32, K+1)
	for k2 := 0; k2 <= K; k2++ {
		pm := make([]int64, m+1)
		pa := make([]int32, m+1)
		pm[0] = Inf
		pa[0] = -1
		row := k2 * n
		for i := 0; i < m; i++ {
			v := fc[row+nodes[i]]
			if v < pm[i] {
				pm[i+1] = v
				pa[i+1] = int32(nodes[i])
			} else {
				pm[i+1] = pm[i]
				pa[i+1] = pa[i]
			}
		}
		prefMin[k2] = pm
		prefArg[k2] = pa
	}

	size := (K + 1) * n
	nxt := make([]int64, size)
	for i := range nxt {
		nxt[i] = Inf
	}
	nxtsrc := make([]byte, size)
	ch := make([]int32, size)
	cb := make([]int64, K+1)
	barg := make([]int32, K+1)

	for a := 0; a < n; a++ {
		s.ops += int64(K+1) * int64(K+1)
		if s.ops > s.MaxOps {
			s.exhausted = true
			return nil, nil, nil
		}
		// childBest[k2]: best child cost compatible with u's nearest being a.
		idx := sort.Search(m, func(i int) bool { return sd[i] > dc[a] })
		aIn := inSub[a]
		for k2 := 0; k2 <= K; k2++ {
			best := prefMin[k2][idx]
			bg := prefArg[k2][idx]
			if !aIn {
				if v := fc[k2*n+a]; v < best {
					best = v
					bg = int32(a)
				}
			}
			cb[k2] = best
			barg[k2] = bg
		}
		for k1 := 0; k1 <= K; k1++ {
			v1 := cur[k1*n+a]
			if v1 == Inf {
				continue
			}
			for k2 := 0; k2 <= K-k1; k2++ {
				v2 := cb[k2]
				if v2 == Inf {
					continue
				}
				cand := v1 + v2
				e := (k1+k2)*n + a
				if cand < nxt[e] {
					nxt[e] = cand
					nxtsrc[e] = cursrc[k1*n+a]
					ch[e] = barg[k2] | int32(k2)<<20
				}
			}
		}
	}
	return nxt, nxtsrc, ch
}

// reconstruct walks the stored choices top-down collecting facility nodes.
func (s *Solver) reconstruct(u, k, a int, facs *[]int) {
	n := s.N
	if s.src[u][k*n+a] == 1 {
		*facs = append(*facs, u)
	}
	rem := k
	for ci := len(s.children[u]) - 1; ci >= 0; ci-- {
		packed := s.choice[u][ci][rem*n+a]
		k2 := int(packed >> 20)
		b := int(packed & (maxDPNodes - 1))
		if k2 > 0 {
			s.reconstruct(s.children[u][ci], k2, b, facs)
		}
		rem -= k2
	}
}

// greedy builds a feasible placement by repeatedly adding the facility that
// most reduces the weighted distance. Used as the budget fallback; its cost
// is a valid upper bound on the optimum.
func (s *Solver) greedy() Result {
	n := s.N
	bestDist := make([]int64, n)
	for i := range bestDist {
		bestDist[i] = Inf
	}
	isFac := make([]bool, n)
	var facs []int
	cur := Inf
	for len(facs) < s.K {
		bestC := -1
		bestCost := cur
		for c := 0; c < n; c++ {
			if s.Forbidden[c] || isFac[c] {
				continue
			}
			var cost int64
			dc := s.dist[c]
			for v := 0; v < n; v++ {
				d := bestDist[v]
				if dc[v] < d {
					d = dc[v]
				}
				cost += s.Demand[v] * d
			}
			if cost < bestCost {
				bestCost = cost
				bestC = c
			}
		}
		if bestC < 0 {
			break
		}
		isFac[bestC] = true
		facs = append(facs, bestC)
		cur = bestCost
		for v := 0; v < n; v++ {
			if d := s.dist[bestC][v]; d < bestDist[v] {
				bestDist[v] = d
			}
		}
	}
	return s.buildResult(facs, false)
}

// buildResult computes the assignment (ties to the smaller facility id) and
// the total cost for a given facility set.
func (s *Solver) buildResult(facs []int, exact bool) Result {
	n := s.N
	assign := make([]int, n)
	if len(facs) == 0 {
		feasible := true
		for v := 0; v < n; v++ {
			assign[v] = -1
			if s.Demand[v] != 0 {
				feasible = false
			}
		}
		return Result{Feasible: feasible, Exact: exact, Facilities: []int{}, Assignment: assign}
	}
	fs := make([]int, len(facs))
	copy(fs, facs)
	sort.Ints(fs)
	var cost int64
	for v := 0; v < n; v++ {
		bestD := Inf
		bestF := -1
		for _, f := range fs {
			d := s.dist[v][f]
			if d < bestD || (d == bestD && f < bestF) {
				bestD = d
				bestF = f
			}
		}
		assign[v] = bestF
		cost += s.Demand[v] * bestD
	}
	return Result{Feasible: true, Exact: exact, Cost: cost, Facilities: fs, Assignment: assign}
}
