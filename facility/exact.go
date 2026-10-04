package main

import "math"

// Exact k-median DP on a rooted tree.
//
// f[v][j][u] = minimum demand-weighted cost of serving subtree(v) with exactly
// j facilities inside subtree(v), given that node v is served by facility u
// (u may lie inside subtree(v) -- then it is one of the j -- or outside).
// Nodes served by u form a connected region containing u; children of v either
// join v's region or are "cut" and partitioned independently (h[c][*]).
//
// h[v][j] = min over t in subtree(v) of f[v][j][t]: subtree(v) partitioned
// standalone with exactly j facilities.
//
// On trees the nearest-facility regions (ties by id) are connected, so
// minimizing over such partitions yields the true k-median optimum.

type exactSolver struct {
	in      *Input
	t       *tree
	demand  []float64
	allowed []bool
	K       int
	dist    [][]float64
	f       [][][]float64 // f[v][j][u]
	h       [][]float64   // h[v][j]
}

func solveExact(in *Input, t *tree, demands []float64, allowed []bool, K int) Output {
	s := &exactSolver{
		in:      in,
		t:       t,
		demand:  demands,
		allowed: allowed,
		K:       K,
	}
	s.computeDist()
	s.dp()
	return s.reconstruct()
}

func (s *exactSolver) computeDist() {
	n := s.t.n
	s.dist = make([][]float64, n)
	for i := range s.dist {
		s.dist[i] = make([]float64, n)
	}
	// BFS/DFS from every source on the weighted tree
	for src := 0; src < n; src++ {
		visited := make([]bool, n)
		visited[src] = true
		stack := []int{src}
		for len(stack) > 0 {
			v := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for _, nb := range s.t.adj[v] {
				if !visited[nb.to] {
					visited[nb.to] = true
					s.dist[src][nb.to] = s.dist[src][v] + nb.w
					stack = append(stack, nb.to)
				}
			}
		}
	}
}

// childOpt returns the per-facility-count option array for child c of v when v
// is served by facility u: min of "cut" (standalone partition of subtree(c))
// and "no cut" (c joins v's region and is served by u). If u lies inside
// subtree(c) the edge must not be cut.
func (s *exactSolver) childOpt(c, u int, opt []float64) {
	K := s.K
	fc := s.f[c]
	hc := s.h[c]
	if s.t.inSubtree(c, u) {
		for j := 0; j <= K; j++ {
			opt[j] = fc[j][u]
		}
		return
	}
	for j := 0; j <= K; j++ {
		a, b := hc[j], fc[j][u]
		if a <= b {
			opt[j] = a
		} else {
			opt[j] = b
		}
	}
}

func (s *exactSolver) dp() {
	n, K := s.t.n, s.K
	s.f = make([][][]float64, n)
	s.h = make([][]float64, n)
	inf := math.Inf(1)

	for _, v := range s.t.order {
		fv := make([][]float64, K+1)
		for j := range fv {
			fv[j] = make([]float64, n)
			for u := range fv[j] {
				fv[j][u] = inf
			}
		}
		s.f[v] = fv
		s.h[v] = make([]float64, K+1)
		for j := range s.h[v] {
			s.h[v][j] = inf
		}

		opt := make([]float64, K+1)
		for u := 0; u < n; u++ {
			uIsV := u == v
			if uIsV && !s.allowed[v] {
				continue
			}
			offset := 0
			if uIsV {
				offset = 1
			}
			// knapsack over children
			dp := make([]float64, K+1)
			for j := range dp {
				dp[j] = inf
			}
			dp[0] = 0
			for _, c := range s.t.children[v] {
				s.childOpt(c, u, opt)
				ndp := make([]float64, K+1)
				for j := range ndp {
					ndp[j] = inf
				}
				for used := 0; used <= K; used++ {
					if math.IsInf(dp[used], 1) {
						continue
					}
					for jc := 0; jc+used <= K; jc++ {
						if math.IsInf(opt[jc], 1) {
							continue
						}
						if val := dp[used] + opt[jc]; val < ndp[used+jc] {
							ndp[used+jc] = val
						}
					}
				}
				dp = ndp
			}
			self := s.demand[v] * s.dist[v][u]
			for j := offset; j <= K; j++ {
				if !math.IsInf(dp[j-offset], 1) {
					fv[j][u] = self + dp[j-offset]
				}
			}
		}
		// h[v][j] = min over facility locations inside subtree(v)
		for u := 0; u < n; u++ {
			if !s.t.inSubtree(v, u) {
				continue
			}
			for j := 0; j <= K; j++ {
				if fv[j][u] < s.h[v][j] {
					s.h[v][j] = fv[j][u]
				}
			}
		}
	}
}

func (s *exactSolver) reconstruct() Output {
	n, K := s.t.n, s.K
	root := 0
	// pick smallest j achieving the optimum
	bestJ := -1
	bestCost := math.Inf(1)
	for j := 0; j <= K; j++ {
		if s.h[root][j] < bestCost-1e-12 {
			bestCost = s.h[root][j]
			bestJ = j
		}
	}
	if bestJ < 0 || math.IsInf(bestCost, 1) {
		return Output{
			Status:      "infeasible",
			Facilities:  []int{},
			Assignments: repeatInt(-1, n),
			TotalCost:   nil,
			Optimal:     false,
		}
	}

	facilities := []int{}
	s.traceStandalone(root, bestJ, &facilities)

	assign, total := assignFromFacilities(s.dist, s.demand, facilities)
	return Output{
		Status:      "optimal",
		Facilities:  facilities,
		Assignments: assign,
		TotalCost:   costPtr(total),
		Optimal:     true,
	}
}

// traceStandalone finds a facility t in subtree(v) attaining h[v][j] and
// traces f[v][j][t].
func (s *exactSolver) traceStandalone(v, j int, facilities *[]int) {
	target := s.h[v][j]
	for u := 0; u < s.t.n; u++ {
		if s.t.inSubtree(v, u) && s.f[v][j][u] == target {
			s.trace(v, j, u, facilities)
			return
		}
	}
}

// trace reconstructs facility placements achieving f[v][j][u].
func (s *exactSolver) trace(v, j, u int, facilities *[]int) {
	K := s.K
	offset := 0
	if u == v {
		*facilities = append(*facilities, v)
		offset = 1
	}
	children := s.t.children[v]
	m := len(children)
	if m == 0 {
		return
	}
	// recompute knapsack tables per prefix of children
	dps := make([][]float64, m+1)
	opts := make([][]float64, m)
	dps[0] = make([]float64, K+1)
	for x := range dps[0] {
		dps[0][x] = math.Inf(1)
	}
	dps[0][0] = 0
	for i, c := range children {
		opt := make([]float64, K+1)
		s.childOpt(c, u, opt)
		opts[i] = opt
		ndp := make([]float64, K+1)
		for x := range ndp {
			ndp[x] = math.Inf(1)
		}
		for used := 0; used <= K; used++ {
			if math.IsInf(dps[i][used], 1) {
				continue
			}
			for jc := 0; jc+used <= K; jc++ {
				if math.IsInf(opt[jc], 1) {
					continue
				}
				if val := dps[i][used] + opt[jc]; val < ndp[used+jc] {
					ndp[used+jc] = val
				}
			}
		}
		dps[i+1] = ndp
	}
	// backtrack allocations child by child
	rem := j - offset
	for i := m - 1; i >= 0; i-- {
		c := children[i]
		chosen := -1
		for jc := 0; jc <= rem; jc++ {
			if math.IsInf(dps[i][rem-jc], 1) || math.IsInf(opts[i][jc], 1) {
				continue
			}
			if dps[i][rem-jc]+opts[i][jc] == dps[i+1][rem] {
				chosen = jc
				break
			}
		}
		if chosen < 0 { // defensive: float equality fallback
			for jc := 0; jc <= rem; jc++ {
				if math.Abs(dps[i][rem-jc]+opts[i][jc]-dps[i+1][rem]) < 1e-9 {
					chosen = jc
					break
				}
			}
		}
		cut := false
		if !s.t.inSubtree(c, u) && s.h[c][chosen] <= s.f[c][chosen][u] {
			cut = true
		}
		if cut {
			s.traceStandalone(c, chosen, facilities)
		} else {
			s.trace(c, chosen, u, facilities)
		}
		rem -= chosen
	}
}
