package frsimp

import "errors"

// Options configures Simplify.
type Options struct {
	// Epsilon is the maximum allowed discrete Frechet distance between the
	// original polyline and the simplification. Must be >= 0.
	Epsilon float64
	// Metric is the distance measure the guarantee refers to.
	// Nil means Euclidean.
	Metric Metric
	// Budget bounds the number of metric evaluations spent on the search.
	// <= 0 means unlimited. When the budget is exhausted, Simplify returns a
	// feasible simplification whose point count is an upper bound on the
	// optimum, with Optimal set to false.
	Budget int64
}

// Result is the outcome of a simplification.
type Result struct {
	// Indices are the retained original vertex indices, strictly increasing,
	// always starting at 0 and ending at len(points)-1.
	Indices []int
	// Matching is the witness: a monotone coupling between the original
	// polyline and Indices whose every pair distance is <= Epsilon.
	Matching []MatchPair
	// Distance is the maximum pair distance over Matching. If DistanceExact
	// is false the evaluation budget ran out while scoring the evidence and
	// Distance is the certified bound Epsilon instead.
	Distance      float64
	DistanceExact bool
	// Optimal is true iff Indices provably has the minimum possible length
	// (ties broken by lexicographic order). False means the budget was
	// exhausted and Indices is a feasible upper bound.
	Optimal bool
	// Evals is the number of metric evaluations consumed.
	Evals int64
}

// evaluator counts metric evaluations against a budget.
type evaluator struct {
	metric Metric
	left   int64 // remaining budget; negative means unlimited
	used   int64
}

func newEvaluator(metric Metric, budget int64) *evaluator {
	left := budget
	if budget <= 0 {
		left = -1
	}
	return &evaluator{metric: metric, left: left}
}

// dist evaluates the metric, reporting false if the budget is exhausted.
func (e *evaluator) dist(a, b Point) (float64, bool) {
	if e.left == 0 {
		return 0, false
	}
	if e.left > 0 {
		e.left--
	}
	e.used++
	return e.metric(a, b), true
}

// state is a search node: the last retained center is v and the placed
// blocks cover the original prefix [0, e].
type state struct{ v, e int }

// exactSearch lazily computes ball-reach rows: reach[w][l] is the largest r
// such that every point in [l, r] lies within eps of points[w] (r = l-1
// means even the singleton is out of ball).
type exactSearch struct {
	points []Point
	eps    float64
	ev     *evaluator
	n      int
	reach  [][]int
	rowOK  []bool
}

// row returns reach[w], computing it with a two-pointer scan (O(n)
// evaluations, amortized). ok is false if the budget ran out.
func (s *exactSearch) row(w int) (row []int, ok bool) {
	if s.rowOK[w] {
		return s.reach[w], true
	}
	n := s.n
	row = make([]int, n)
	r := -1
	for l := 0; l < n; l++ {
		if r < l-1 {
			r = l - 1
		}
		for r+1 < n {
			d, ok := s.ev.dist(s.points[w], s.points[r+1])
			if !ok {
				return nil, false
			}
			if d > s.eps {
				break
			}
			r++
		}
		row[l] = r
	}
	s.reach[w] = row
	s.rowOK[w] = true
	return row, true
}

// Simplify reduces points to a subsequence of its vertices, keeping the
// first and last point, such that the discrete Frechet distance (under
// opts.Metric) between points and the subsequence is at most opts.Epsilon.
//
// The criterion is the genuine discrete Frechet distance, decided through
// the following equivalence (no perpendicular-distance proxy is involved):
// a subsequence with centers c_1=0 < c_2 < ... < c_m=n-1 has dF <= eps iff
// [0, n-1] partitions into m consecutive nonempty blocks, block s lying
// entirely within eps of points[c_s]. Both directions are constructive, and
// the block decomposition is exactly the returned Matching.
//
// Minimizing m is a level-by-level BFS over states (last center, covered
// prefix) with Pareto-frontier pruning; among minimum-m solutions a greedy
// pass over backward-computed completion sets yields the lexicographically
// smallest center sequence. When opts.Budget cuts the search short, a
// greedy-plus-identity fallback returns a feasible upper bound instead.
func Simplify(points []Point, opts Options) (Result, error) {
	n := len(points)
	if n == 0 {
		return Result{}, errors.New("frsimp: empty polyline")
	}
	if opts.Epsilon < 0 {
		return Result{}, errors.New("frsimp: negative epsilon")
	}
	metric := opts.Metric
	if metric == nil {
		metric = Euclidean
	}
	ev := newEvaluator(metric, opts.Budget)

	if n <= 2 {
		res := Result{Optimal: true, DistanceExact: true}
		for i := 0; i < n; i++ {
			res.Indices = append(res.Indices, i)
			res.Matching = append(res.Matching, MatchPair{Orig: i, Simp: i})
		}
		return res, nil
	}

	res, ok := exactSimplify(points, opts.Epsilon, ev)
	if !ok {
		res = fallbackSimplify(points, opts.Epsilon, ev)
	}
	res.Evals = ev.used
	return res, nil
}

// exactSimplify runs the optimal search; ok is false if the budget was
// exhausted, in which case the caller falls back.
func exactSimplify(points []Point, eps float64, ev *evaluator) (res Result, ok bool) {
	n := len(points)
	s := &exactSearch{points: points, eps: eps, ev: ev, n: n, reach: make([][]int, n), rowOK: make([]bool, n)}

	row0, ok := s.row(0)
	if !ok {
		return Result{}, false
	}
	// llast: smallest l with [l, n-1] within eps of the last point.
	llast := n - 1
	for llast-1 >= 0 {
		d, ok := ev.dist(points[n-1], points[llast-1])
		if !ok {
			return Result{}, false
		}
		if d > eps {
			break
		}
		llast--
	}

	// Forward BFS by block count. frontiers[k] is the Pareto frontier
	// (v ascending, e strictly ascending) after k+1 blocks.
	frontiers := [][]state{{{v: 0, e: row0[0]}}}
	m := 0
	for {
		F := frontiers[len(frontiers)-1]
		if F[len(F)-1].e >= llast-1 {
			m = len(frontiers) + 1
			break
		}
		cand := make([]int, n) // best coverage per next center; -1 = none
		for i := range cand {
			cand[i] = -1
		}
		for _, st := range F {
			for w := st.v + 1; w <= n-2; w++ {
				rw, ok := s.row(w)
				if !ok {
					return Result{}, false
				}
				if e2 := rw[st.e+1]; e2 >= st.e+1 && e2 > cand[w] {
					cand[w] = e2
				}
			}
		}
		var NF []state
		maxE := -1
		for w := 0; w <= n-2; w++ {
			if cand[w] > maxE {
				NF = append(NF, state{v: w, e: cand[w]})
				maxE = cand[w]
			}
		}
		if len(NF) == 0 {
			return Result{}, false // unreachable: singleton blocks always work
		}
		frontiers = append(frontiers, NF)
	}

	// Backward completion sets. C[t] holds the Pareto-minimal states at
	// block count t from which the remaining m-t blocks can still be placed.
	// A state completes iff it dominates some Y in C[t+1]
	// (v <= Y.v and e >= Y.e): dominance lets it mimic Y's completion.
	//
	// Every block must be nonempty, so a level-t state may use at most
	// cap(t) = n-1-(m-t) coverage, leaving one point per remaining block.
	// Any coverage below the achieved maximum is reachable by shrinking
	// blocks (sub-intervals stay within the ball), so capping is sound.
	capAt := func(t int) int { return n - 1 - (m - t) }
	for t := 1; t <= m-1; t++ {
		F := frontiers[t-1]
		pruned := F[:0]
		maxE := -1
		for _, st := range F {
			if st.e > capAt(t) {
				st.e = capAt(t)
			}
			if st.e > maxE {
				pruned = append(pruned, st)
				maxE = st.e
			}
		}
		frontiers[t-1] = pruned
	}
	C := make([][]state, m)
	for _, st := range frontiers[m-2] {
		if st.e >= llast-1 {
			C[m-1] = append(C[m-1], st)
		}
	}
	for t := m - 2; t >= 1; t-- {
		for _, st := range frontiers[t-1] {
			if completes(s, st, C[t+1]) {
				C[t] = append(C[t], st)
			}
		}
	}

	// Greedy lexicographic reconstruction: at each block pick the smallest
	// center whose resulting state still dominates a completion state.
	centers := []int{0}
	first := row0[0]
	if first > capAt(1) {
		first = capAt(1)
	}
	ends := []int{first}
	for lvl := 1; lvl <= m-2; lvl++ {
		e := ends[lvl-1]
		chosen, chosenE := -1, -1
		for w := centers[lvl-1] + 1; w <= n-2; w++ {
			ye, ok := suffixMinE(C[lvl+1], w)
			if !ok {
				break // no completion state at v >= w, nor beyond
			}
			rw, ok := s.row(w)
			if !ok {
				return Result{}, false
			}
			ne := rw[e+1]
			if ne > capAt(lvl+1) {
				ne = capAt(lvl + 1)
			}
			if ne >= ye {
				chosen, chosenE = w, ne
				break
			}
		}
		if chosen < 0 {
			return Result{}, false // unreachable by the completion invariant
		}
		centers = append(centers, chosen)
		ends = append(ends, chosenE)
	}
	centers = append(centers, n-1)
	ends = append(ends, n-1)

	res = Result{Indices: centers, Optimal: true}
	emitMatching(points, centers, ends, eps, ev, &res)
	return res, true
}

// completes reports whether st can still finish: some next center w in
// (st.v, Y.v] reaches coverage >= Y.e for a completion state Y.
func completes(s *exactSearch, st state, CY []state) bool {
	for _, y := range CY {
		if y.v <= st.v {
			continue
		}
		for w := st.v + 1; w <= y.v; w++ {
			rw, ok := s.row(w)
			if !ok {
				return false // budget ran out; caller aborts anyway
			}
			if rw[st.e+1] >= y.e {
				return true
			}
		}
	}
	return false
}

// suffixMinE returns the minimum e among completion states with v >= w.
// CY is sorted by v ascending and (being an antichain) e ascending, so the
// first qualifying state has the minimum e.
func suffixMinE(CY []state, w int) (int, bool) {
	for _, y := range CY {
		if y.v >= w {
			return y.e, true
		}
	}
	return 0, false
}

// fallbackSimplify returns a feasible simplification when the budget stopped
// the exact search: greedy farthest-coverage blocks while evaluations
// remain, then the identity subsequence (singleton blocks, zero error).
// Its length is an upper bound on the optimum.
func fallbackSimplify(points []Point, eps float64, ev *evaluator) Result {
	n := len(points)
	s := &exactSearch{points: points, eps: eps, ev: ev, n: n, reach: make([][]int, n), rowOK: make([]bool, n)}

	centers := []int{0}
	e := 0
	if row0, ok := s.row(0); ok {
		e = row0[0]
	}
	if e > n-2 {
		e = n - 2 // leave room for the mandatory final center n-1
	}
	ends := []int{e}
	v := 0
	// Invariant v <= e keeps the identity tail feasible.
	for e < n-2 {
		bestW, bestE := -1, -1
		for w := v + 1; w <= n-2; w++ {
			rw, ok := s.row(w)
			if !ok {
				break
			}
			e2 := rw[e+1]
			if e2 > n-2 {
				e2 = n - 2
			}
			if e2 >= e+1 && e2 >= w && e2 > bestE {
				bestW, bestE = w, e2
			}
		}
		if bestW < 0 {
			break
		}
		centers = append(centers, bestW)
		ends = append(ends, bestE)
		v, e = bestW, bestE
	}
	for a := e + 1; a <= n-1; a++ {
		centers = append(centers, a)
		ends = append(ends, a)
	}
	res := Result{Indices: centers, Optimal: false}
	emitMatching(points, centers, ends, eps, ev, &res)
	return res
}

// emitMatching turns the block decomposition into a monotone coupling:
// block s covers original indices (ends[s-1], ends[s]] and is matched to
// center s. Every pair is within eps by construction; if the budget runs
// out while scoring, Distance falls back to the certified bound eps.
func emitMatching(points []Point, centers, ends []int, eps float64, ev *evaluator, res *Result) {
	var matching []MatchPair
	prev := -1
	for s := 0; s < len(centers); s++ {
		for a := prev + 1; a <= ends[s]; a++ {
			matching = append(matching, MatchPair{Orig: a, Simp: s})
		}
		prev = ends[s]
	}
	exact := true
	maxd := 0.0
	for _, p := range matching {
		d, ok := ev.dist(points[p.Orig], points[centers[p.Simp]])
		if !ok {
			exact = false
			continue
		}
		if d > maxd {
			maxd = d
		}
	}
	if !exact {
		maxd = eps
	}
	res.Matching = matching
	res.Distance = maxd
	res.DistanceExact = exact
}
