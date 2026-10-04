// Package segsolver finds minimum-cost contiguous segmentations of a
// sequence under segment-length bounds, mandatory cut positions, and
// forbidden cut positions.
//
// A sequence of length n has internal cut positions 1..n-1 (position p
// sits between elements p-1 and p). Every segmentation must cut at each
// mandatory position, must not cut at any forbidden position, and every
// segment length must lie in [MinLen, MaxLen]. The cost of a segment is
// looked up in an integer table indexed by segment length, so segment
// costs may be negative.
//
// Among all minimum-cost segmentations the solver returns the one whose
// ascending cut list is lexicographically smallest (a shorter list that
// is a prefix of a longer one counts as smaller).
package segsolver

import (
	"fmt"
	"sort"
)

// Result describes the outcome of a solve.
type Result struct {
	// Feasible reports whether at least one legal segmentation exists.
	Feasible bool
	// Value is the minimum total cost. Valid only when Feasible is true.
	Value int64
	// Cuts is the evidence for Value: the lexicographically smallest
	// ascending list of cut positions among all optimal segmentations.
	// Valid only when Feasible is true.
	Cuts []int
	// WithinBudget reports whether Value does not exceed the budget
	// passed to SolveWithBudget. It is always true for Solve.
	//
	// When the budget is insufficient (WithinBudget == false), Cuts and
	// Value still describe the cheapest legal segmentation, i.e. a legal
	// upper bound on what any budget-constrained answer could achieve.
	WithinBudget bool
}

// Solver holds a segmentation problem instance. The cost table can be
// revised locally with Revise; results are recomputed lazily on the next
// Solve call.
type Solver struct {
	n      int
	minLen int
	maxLen int
	cost   []int64 // cost[l] is the cost of a segment of length l

	must   map[int]bool
	forbid map[int]bool

	// prevMust[j] is the largest mandatory position < j (0 if none):
	// a segment (i, j) is legal only if i >= prevMust[j].
	prevMust []int
	// nextMust[i] is the smallest mandatory position > i (n if none):
	// a segment (i, j) is legal only if j <= nextMust[i].
	nextMust []int

	dirty  bool
	dp     []int64 // dp[j]  = min cost of segmenting the prefix [0, j)
	reach  []bool  // reach[j] reports whether dp[j] is attainable
	suff   []int64 // suff[i] = min cost of segmenting the suffix [i, n)
	sreach []bool  // sreach[i] reports whether suff[i] is attainable
}

// New builds a solver for a sequence of length n. cost[l] must hold the
// cost of a segment of length l for minLen <= l <= maxLen, so the table
// needs at least maxLen+1 entries; it is copied internally. must and
// forbid list mandatory and forbidden cut positions, each in [1, n-1].
// A position that is both mandatory and forbidden is a conflict and
// makes the instance unsatisfiable; New returns an error naming all
// conflicting positions.
func New(n, minLen, maxLen int, cost []int64, must, forbid []int) (*Solver, error) {
	if n < 0 {
		return nil, fmt.Errorf("segsolver: negative sequence length %d", n)
	}
	if minLen < 1 || maxLen < minLen {
		return nil, fmt.Errorf("segsolver: invalid segment length bounds [%d, %d]", minLen, maxLen)
	}
	if len(cost) <= maxLen {
		return nil, fmt.Errorf("segsolver: cost table has %d entries, need index up to %d", len(cost), maxLen)
	}
	s := &Solver{
		n:      n,
		minLen: minLen,
		maxLen: maxLen,
		cost:   append([]int64(nil), cost...),
		must:   make(map[int]bool),
		forbid: make(map[int]bool),
		dirty:  true,
	}
	for _, p := range must {
		if p < 1 || p > n-1 {
			return nil, fmt.Errorf("segsolver: mandatory cut %d out of range [1, %d]", p, n-1)
		}
		s.must[p] = true
	}
	for _, p := range forbid {
		if p < 1 || p > n-1 {
			return nil, fmt.Errorf("segsolver: forbidden cut %d out of range [1, %d]", p, n-1)
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
		return nil, fmt.Errorf("segsolver: positions %v are both mandatory and forbidden", conflicts)
	}

	s.prevMust = make([]int, n+1)
	for j := 1; j <= n; j++ {
		s.prevMust[j] = s.prevMust[j-1]
		if s.must[j-1] {
			s.prevMust[j] = j - 1
		}
	}
	s.nextMust = make([]int, n+1)
	s.nextMust[n] = n
	for i := n - 1; i >= 0; i-- {
		s.nextMust[i] = s.nextMust[i+1]
		if s.must[i+1] {
			s.nextMust[i] = i + 1
		}
	}
	return s, nil
}

// Revise applies local cost-table revisions: updates maps a segment
// length to its new cost. The next Solve recomputes from the revised
// table. An out-of-range length is rejected and no entry is changed.
func (s *Solver) Revise(updates map[int]int64) error {
	for l := range updates {
		if l < s.minLen || l > s.maxLen {
			return fmt.Errorf("segsolver: cannot revise cost of length %d outside [%d, %d]", l, s.minLen, s.maxLen)
		}
	}
	for l, c := range updates {
		s.cost[l] = c
	}
	if len(updates) > 0 {
		s.dirty = true
	}
	return nil
}

// Solve returns the minimum-cost legal segmentation.
func (s *Solver) Solve() Result {
	s.compute()
	if !s.reach[s.n] {
		return Result{Feasible: false, WithinBudget: true}
	}
	return Result{
		Feasible:     true,
		Value:        s.dp[s.n],
		Cuts:         s.reconstruct(),
		WithinBudget: true,
	}
}

// SolveWithBudget solves against a cost budget. The result always
// carries the optimum; when the budget is insufficient, WithinBudget is
// false but the returned cuts remain the cheapest legal segmentation,
// serving as a legal upper-bound certificate.
func (s *Solver) SolveWithBudget(budget int64) Result {
	r := s.Solve()
	if r.Feasible {
		r.WithinBudget = r.Value <= budget
	}
	return r
}

// Check validates proposed cut evidence and returns its total cost. It
// verifies that cuts are strictly increasing internal positions, include
// every mandatory cut, avoid every forbidden position, and that all
// resulting segment lengths lie within the configured bounds.
func (s *Solver) Check(cuts []int) (int64, error) {
	if s.n == 0 {
		if len(cuts) != 0 {
			return 0, fmt.Errorf("segsolver: cut %d out of range for empty sequence", cuts[0])
		}
		return 0, nil
	}
	for i, p := range cuts {
		if p < 1 || p > s.n-1 {
			return 0, fmt.Errorf("segsolver: cut %d out of range [1, %d]", p, s.n-1)
		}
		if i > 0 && p <= cuts[i-1] {
			return 0, fmt.Errorf("segsolver: cuts not strictly increasing at %d", p)
		}
		if s.forbid[p] {
			return 0, fmt.Errorf("segsolver: cut at forbidden position %d", p)
		}
	}
	for p := range s.must {
		if i := sort.SearchInts(cuts, p); i >= len(cuts) || cuts[i] != p {
			return 0, fmt.Errorf("segsolver: missing mandatory cut at %d", p)
		}
	}
	total := int64(0)
	prev := 0
	for _, b := range append(append([]int(nil), cuts...), s.n) {
		l := b - prev
		if l < s.minLen || l > s.maxLen {
			return 0, fmt.Errorf("segsolver: segment length %d outside [%d, %d]", l, s.minLen, s.maxLen)
		}
		total += s.cost[l]
		prev = b
	}
	return total, nil
}

// compute runs the forward and backward dynamic programs if the cost
// table changed since the last run. Both are O(n * (maxLen-minLen+1)).
func (s *Solver) compute() {
	if !s.dirty {
		return
	}
	n := s.n
	s.dp = make([]int64, n+1)
	s.reach = make([]bool, n+1)
	s.reach[0] = true
	for j := 1; j <= n; j++ {
		lo := j - s.maxLen
		if lo < s.prevMust[j] {
			lo = s.prevMust[j]
		}
		if lo < 0 {
			lo = 0
		}
		hi := j - s.minLen
		best := int64(0)
		ok := false
		for i := lo; i <= hi; i++ {
			if !s.reach[i] || (i != 0 && s.forbid[i]) {
				continue
			}
			v := s.dp[i] + s.cost[j-i]
			if !ok || v < best {
				best, ok = v, true
			}
		}
		if ok {
			s.dp[j], s.reach[j] = best, true
		}
	}

	s.suff = make([]int64, n+1)
	s.sreach = make([]bool, n+1)
	s.sreach[n] = true
	for i := n - 1; i >= 0; i-- {
		lo := i + s.minLen
		hi := i + s.maxLen
		if hi > s.nextMust[i] {
			hi = s.nextMust[i]
		}
		if hi > n {
			hi = n
		}
		best := int64(0)
		ok := false
		for j := lo; j <= hi; j++ {
			if !s.sreach[j] || (j != n && s.forbid[j]) {
				continue
			}
			v := s.cost[j-i] + s.suff[j]
			if !ok || v < best {
				best, ok = v, true
			}
		}
		if ok {
			s.suff[i], s.sreach[i] = best, true
		}
	}
	s.dirty = false
}

// reconstruct greedily builds the lexicographically smallest optimal cut
// list. At each step it prefers stopping (a shorter list is a prefix of
// any extension, hence lexicographically smaller); otherwise it takes
// the smallest next cut that can still reach the optimum. Correctness
// relies on the suffix optimum table: a choice is kept exactly when the
// remaining cost still allows an optimal completion.
func (s *Solver) reconstruct() []int {
	cuts := []int{}
	pos := 0
	remaining := s.dp[s.n]
	for pos < s.n {
		l := s.n - pos
		if l >= s.minLen && l <= s.maxLen && s.nextMust[pos] >= s.n && s.cost[l] == remaining {
			break
		}
		hi := pos + s.maxLen
		if hi > s.nextMust[pos] {
			hi = s.nextMust[pos]
		}
		if hi > s.n-1 {
			hi = s.n - 1
		}
		found := false
		for p := pos + s.minLen; p <= hi; p++ {
			if s.forbid[p] || !s.sreach[p] {
				continue
			}
			if s.cost[p-pos]+s.suff[p] == remaining {
				cuts = append(cuts, p)
				remaining -= s.cost[p-pos]
				pos = p
				found = true
				break
			}
		}
		if !found {
			panic("segsolver: internal error, cannot reconstruct optimal cuts")
		}
	}
	return cuts
}
