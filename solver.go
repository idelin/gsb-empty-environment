// Package segsolver finds minimum-cost segmentations of a sequence under
// segment-length limits, mandatory and forbidden cut positions, and an
// integer segment-cost table. Among minimum-cost segmentations it picks the
// one whose sorted cut-position list is lexicographically smallest. It
// supports local cost revision, budget checks that keep a legal upper bound,
// and brute-force cross-verification on small sequences.
package segsolver

import (
	"fmt"
	"sort"
)

// inf is a sentinel larger than any reachable total cost.
const inf = int64(1) << 62

// Solver holds one problem instance: sequence length n (positions 0..n, cuts
// may fall at interior positions 1..n-1), segment-length limits, an integer
// cost table, and mandatory / forbidden cut positions.
type Solver struct {
	n          int
	minLen     int
	maxLen     int
	cost       [][]int64
	must       map[int]bool
	forbid     map[int]bool
	mustSorted []int
}

// Result is the outcome of a Solve call.
type Result struct {
	Feasible bool   // a legal segmentation exists
	Cost     int64  // minimum total cost (valid when Feasible)
	Cuts     []int  // lexicographically smallest min-cost cut list, ascending
	Reason   string // human-readable note for infeasible / over-budget cases

	HasBudget    bool  // whether a budget was supplied
	Budget       int64 // the supplied budget
	WithinBudget bool  // Cost <= Budget; false also when infeasible
}

// cutList is a persistent ascending list of cut positions. Prepending is
// O(1), which keeps the backward DP cheap.
type cutList struct {
	pos  int
	next *cutList
}

// lessCuts reports whether a is lexicographically smaller than b; a strict
// prefix counts as smaller (e.g. [2] < [2,5]).
func lessCuts(a, b *cutList) bool {
	for a != nil && b != nil {
		if a.pos != b.pos {
			return a.pos < b.pos
		}
		a, b = a.next, b.next
	}
	return a == nil && b != nil
}

func (l *cutList) toSlice() []int {
	var out []int
	for p := l; p != nil; p = p.next {
		out = append(out, p.pos)
	}
	return out
}

// NewSolver builds a solver for a sequence of length n. cost must be an
// (n+1)x(n+1) table; only entries cost[i][j] with minLen <= j-i <= maxLen are
// read, and they may be negative. must and forbid list interior cut positions
// (1..n-1) that are required resp. prohibited as segment boundaries. A
// position that is both mandatory and forbidden is a conflict and is
// reported in the returned error.
func NewSolver(n, minLen, maxLen int, cost [][]int64, must, forbid []int) (*Solver, error) {
	if n < 1 {
		return nil, fmt.Errorf("segsolver: n must be >= 1, got %d", n)
	}
	if minLen < 1 || maxLen < minLen || maxLen > n {
		return nil, fmt.Errorf("segsolver: invalid segment length limits [%d,%d] for n=%d", minLen, maxLen, n)
	}
	if len(cost) != n+1 {
		return nil, fmt.Errorf("segsolver: cost table must have %d rows, got %d", n+1, len(cost))
	}
	for i, row := range cost {
		if len(row) != n+1 {
			return nil, fmt.Errorf("segsolver: cost row %d must have %d entries, got %d", i, n+1, len(row))
		}
	}
	s := &Solver{
		n:      n,
		minLen: minLen,
		maxLen: maxLen,
		cost:   make([][]int64, n+1),
		must:   make(map[int]bool),
		forbid: make(map[int]bool),
	}
	for i := range cost {
		row := make([]int64, n+1)
		copy(row, cost[i])
		s.cost[i] = row
	}
	checkPos := func(p int) error {
		if p < 1 || p > n-1 {
			return fmt.Errorf("segsolver: cut position %d out of interior range [1,%d]", p, n-1)
		}
		return nil
	}
	for _, p := range must {
		if err := checkPos(p); err != nil {
			return nil, err
		}
		s.must[p] = true
	}
	for _, p := range forbid {
		if err := checkPos(p); err != nil {
			return nil, err
		}
		s.forbid[p] = true
	}
	var conflicts []int
	for p := range s.must {
		if s.forbid[p] {
			conflicts = append(conflicts, p)
		}
	}
	if len(conflicts) > 0 {
		sort.Ints(conflicts)
		return nil, fmt.Errorf("segsolver: conflicting cut positions (both mandatory and forbidden): %v", conflicts)
	}
	for p := range s.must {
		s.mustSorted = append(s.mustSorted, p)
	}
	sort.Ints(s.mustSorted)
	return s, nil
}

// Revise applies a local cost revision: it replaces the cost of segment
// [i,j). The next Solve reflects the new value.
func (s *Solver) Revise(i, j int, c int64) error {
	if i < 0 || i >= j || j > s.n {
		return fmt.Errorf("segsolver: invalid segment [%d,%d) for n=%d", i, j, s.n)
	}
	if l := j - i; l < s.minLen || l > s.maxLen {
		return fmt.Errorf("segsolver: segment [%d,%d) length %d outside [%d,%d]", i, j, l, s.minLen, s.maxLen)
	}
	s.cost[i][j] = c
	return nil
}

// crossesMust reports whether some mandatory cut lies strictly inside (i,j).
func (s *Solver) crossesMust(i, j int) bool {
	k := sort.SearchInts(s.mustSorted, i+1)
	return k < len(s.mustSorted) && s.mustSorted[k] < j
}

// Solve returns the minimum-cost legal segmentation.
func (s *Solver) Solve() Result { return s.solve(0, false) }

// SolveWithBudget behaves like Solve but also checks the optimum against
// budget. When the budget is insufficient the returned Result still carries
// the cheapest legal segmentation: any legal cost is a valid upper bound on
// the optimum, so the evidence is preserved instead of being dropped.
func (s *Solver) SolveWithBudget(budget int64) Result { return s.solve(budget, true) }

// solve runs a backward DP. Backward (prepending cuts) is required for the
// lexicographic tie-break: prepending a common prefix preserves lexicographic
// order, whereas appending a common suffix does not when one list is a
// prefix of the other.
func (s *Solver) solve(budget int64, hasBudget bool) Result {
	n := s.n
	dp := make([]int64, n+1)      // dp[i] = min cost of segmenting [i,n)
	best := make([]*cutList, n+1) // cuts achieving dp[i]
	ok := make([]bool, n+1)
	ok[n] = true
	for i := n - 1; i >= 0; i-- {
		if i > 0 && s.forbid[i] {
			continue // no boundary allowed at i
		}
		bestCost := inf
		var bestCuts *cutList
		for j := i + s.minLen; j <= i+s.maxLen && j <= n; j++ {
			if !ok[j] || s.crossesMust(i, j) {
				continue
			}
			c := s.cost[i][j] + dp[j]
			cand := best[j]
			if j < n {
				cand = &cutList{pos: j, next: best[j]}
			}
			if c < bestCost || (c == bestCost && lessCuts(cand, bestCuts)) {
				bestCost, bestCuts = c, cand
			}
		}
		if bestCost < inf {
			ok[i], dp[i], best[i] = true, bestCost, bestCuts
		}
	}
	res := Result{HasBudget: hasBudget, Budget: budget}
	if !ok[0] {
		res.Reason = "no legal segmentation satisfies the length and cut constraints"
		return res
	}
	res.Feasible = true
	res.Cost = dp[0]
	res.Cuts = best[0].toSlice()
	res.WithinBudget = !hasBudget || res.Cost <= budget
	if hasBudget && !res.WithinBudget {
		res.Reason = fmt.Sprintf("budget %d insufficient; cheapest legal cut costs %d and is kept as a valid upper bound", budget, res.Cost)
	}
	return res
}

// Evaluate checks that cuts is a legal segmentation and returns its total
// cost. It is the evidence checker: a Result's Cuts always evaluate to its
// Cost. cuts must be strictly ascending interior positions, include every
// mandatory cut, avoid every forbidden cut, and respect the length limits.
func (s *Solver) Evaluate(cuts []int) (int64, error) {
	for k, c := range cuts {
		if c < 1 || c > s.n-1 {
			return 0, fmt.Errorf("segsolver: cut %d out of interior range [1,%d]", c, s.n-1)
		}
		if k > 0 && cuts[k-1] >= c {
			return 0, fmt.Errorf("segsolver: cuts %v not strictly ascending", cuts)
		}
		if s.forbid[c] {
			return 0, fmt.Errorf("segsolver: cut %d is forbidden", c)
		}
	}
	cutSet := make(map[int]bool, len(cuts))
	for _, c := range cuts {
		cutSet[c] = true
	}
	for _, m := range s.mustSorted {
		if !cutSet[m] {
			return 0, fmt.Errorf("segsolver: mandatory cut %d missing", m)
		}
	}
	total := int64(0)
	prev := 0
	for _, b := range append(append([]int(nil), cuts...), s.n) {
		if l := b - prev; l < s.minLen || l > s.maxLen {
			return 0, fmt.Errorf("segsolver: segment [%d,%d) length %d outside [%d,%d]", prev, b, l, s.minLen, s.maxLen)
		}
		total += s.cost[prev][b]
		prev = b
	}
	return total, nil
}

// VerifyBruteForce enumerates all 2^(n-1) cut subsets, recomputes the optimum
// (including any negative segment costs) exactly, and compares it against the
// DP. It is intended for small sequences (n <= 20) and returns a descriptive
// error on any mismatch.
func (s *Solver) VerifyBruteForce() error {
	if s.n > 20 {
		return fmt.Errorf("segsolver: brute-force verification limited to n<=20, got n=%d", s.n)
	}
	feas, cost, cuts := s.bruteForce()
	res := s.Solve()
	switch {
	case feas != res.Feasible:
		return fmt.Errorf("segsolver: feasibility mismatch: dp=%v brute=%v", res.Feasible, feas)
	case !feas:
		return nil
	case cost != res.Cost:
		return fmt.Errorf("segsolver: cost mismatch: dp=%d brute=%d", res.Cost, cost)
	case !equalInts(cuts, res.Cuts):
		return fmt.Errorf("segsolver: cut mismatch: dp=%v brute=%v", res.Cuts, cuts)
	}
	return nil
}

// bruteForce enumerates every legal cut subset and returns the minimum cost
// with the lexicographically smallest cut list.
func (s *Solver) bruteForce() (feasible bool, bestCost int64, bestCuts []int) {
	var optional []int
	for p := 1; p < s.n; p++ {
		if !s.must[p] && !s.forbid[p] {
			optional = append(optional, p)
		}
	}
	bestCost = inf
	for mask := 0; mask < 1<<uint(len(optional)); mask++ {
		cuts := append([]int(nil), s.mustSorted...)
		for k, p := range optional {
			if mask&(1<<uint(k)) != 0 {
				cuts = append(cuts, p)
			}
		}
		sort.Ints(cuts)
		cost, err := s.Evaluate(cuts)
		if err != nil {
			continue
		}
		if !feasible || cost < bestCost || (cost == bestCost && lexLess(cuts, bestCuts)) {
			feasible, bestCost, bestCuts = true, cost, cuts
		}
	}
	return feasible, bestCost, bestCuts
}

// lexLess compares cut lists lexicographically; a strict prefix is smaller.
func lexLess(a, b []int) bool {
	for k := 0; k < len(a) && k < len(b); k++ {
		if a[k] != b[k] {
			return a[k] < b[k]
		}
	}
	return len(a) < len(b)
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if a[k] != b[k] {
			return false
		}
	}
	return true
}
