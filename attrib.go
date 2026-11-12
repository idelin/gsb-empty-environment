package netsec

import (
	"errors"
	"fmt"
	"math/bits"
	"sort"
)

// Set is a bitmask over mutation indices 0..N-1.
type Set uint64

// Contains reports whether every mutation in o is also in s.
func (s Set) Contains(o Set) bool { return s&o == o }

// Indices returns the mutation indices present in s, in ascending order.
func (s Set) Indices() []int {
	out := make([]int, 0, bits.OnesCount64(uint64(s)))
	for i := 0; i < 64; i++ {
		if s&(1<<uint(i)) != 0 {
			out = append(out, i)
		}
	}
	return out
}

// SetOf builds a Set from mutation indices.
func SetOf(indices ...int) Set {
	var s Set
	for _, i := range indices {
		s |= 1 << uint(i)
	}
	return s
}

// Problem describes one attribution instance. Fail must be deterministic:
// identical feasible inputs must always yield identical results. Fail is
// never invoked with an infeasible set.
type Problem struct {
	// N is the number of candidate mutations (0..64).
	N int
	// Conflicts lists mutation pairs that must never co-occur in a set.
	Conflicts [][2]int
	// Requires lists directed dependencies: {A, B} means any set containing
	// A must also contain B. Cycles collapse into take-all-or-none groups.
	Requires [][2]int
	// Fail reports whether the given feasible mutation set triggers the fault.
	Fail func(Set) bool
}

// Status classifies a mutation set with respect to the search progress.
type Status int

const (
	// StatusInfeasible marks sets violating exclusion or dependency rules.
	StatusInfeasible Status = iota
	// StatusProvenMinimal marks sets proven to fail and inclusion-minimal.
	StatusProvenMinimal
	// StatusDecided marks feasible sets already ruled out: either evaluated
	// and passing, or strict supersets of a proven minimal failing set
	// (hence certainly not minimal, whatever their own outcome).
	StatusDecided
	// StatusUnsearched marks feasible sets inside the unexplored region.
	StatusUnsearched
)

func (st Status) String() string {
	switch st {
	case StatusInfeasible:
		return "infeasible"
	case StatusProvenMinimal:
		return "proven-minimal"
	case StatusDecided:
		return "decided"
	case StatusUnsearched:
		return "unsearched"
	}
	return "unknown"
}

// Result is a snapshot of a Searcher. When Complete is false the budget was
// exhausted; Minimal still contains only fully proven minimal failing sets,
// and the unsearched region is exactly the feasible sets of cardinality
// Level generated after Cursor (in Gosper order) plus every feasible set of
// cardinality greater than Level, in both cases excluding supersets of any
// set in Minimal. Use Classify or ForEachUnsearched to inspect it.
type Result struct {
	// Minimal holds every proven inclusion-minimal failing set found so
	// far, in discovery order (ascending cardinality, then bitmask).
	Minimal []Set
	// Evals is the total number of Fail invocations so far.
	Evals int
	// Complete reports whether the entire feasible lattice was searched.
	Complete bool
	// Level is the cardinality currently being enumerated.
	Level int
	// Cursor is the most recently generated candidate mask at Level.
	Cursor Set
	// HasCursor reports whether any candidate at Level was generated yet.
	HasCursor bool
}

// Searcher enumerates minimal failing sets for one Problem. It is stateful:
// call Run repeatedly with additional budget to resume exactly where the
// previous call stopped. A Searcher is not safe for concurrent use.
type Searcher struct {
	n         int
	conflict  []Set // conflict[i]: mutations incompatible with i
	requires  []Set // requires[i]: transitive prerequisites of i
	fail      func(Set) bool
	minimal   []Set
	evals     int
	level     int
	cursor    Set
	hasCursor bool
	done      bool
}

// NewSearcher validates p and prepares the enumeration.
func NewSearcher(p Problem) (*Searcher, error) {
	if p.N < 0 || p.N > 64 {
		return nil, fmt.Errorf("attrib: N=%d out of range [0,64]", p.N)
	}
	if p.Fail == nil {
		return nil, errors.New("attrib: Fail callback is nil")
	}
	s := &Searcher{
		n:        p.N,
		conflict: make([]Set, p.N),
		requires: make([]Set, p.N),
		fail:     p.Fail,
	}
	check := func(pair [2]int) error {
		for _, idx := range pair {
			if idx < 0 || idx >= p.N {
				return fmt.Errorf("attrib: mutation index %d out of range [0,%d)", idx, p.N)
			}
		}
		return nil
	}
	for _, c := range p.Conflicts {
		if err := check(c); err != nil {
			return nil, err
		}
		s.conflict[c[0]] |= 1 << uint(c[1])
		s.conflict[c[1]] |= 1 << uint(c[0])
	}
	for _, r := range p.Requires {
		if err := check(r); err != nil {
			return nil, err
		}
		s.requires[r[0]] |= 1 << uint(r[1])
	}
	// Transitive closure of the dependency relation.
	for changed := true; changed; {
		changed = false
		for i := 0; i < p.N; i++ {
			closure := s.requires[i]
			for m := closure; m != 0; m &= m - 1 {
				j := bits.TrailingZeros64(uint64(m))
				closure |= s.requires[j]
			}
			if closure != s.requires[i] {
				s.requires[i] = closure
				changed = true
			}
		}
	}
	return s, nil
}

// Feasible reports whether s respects all exclusion and dependency rules.
func (s *Searcher) Feasible(set Set) bool {
	if s.n < 64 && set>>(uint(s.n)) != 0 {
		return false
	}
	for m := set; m != 0; m &= m - 1 {
		i := bits.TrailingZeros64(uint64(m))
		if s.conflict[i]&set != 0 {
			return false
		}
		if s.requires[i]&^set != 0 {
			return false
		}
	}
	return true
}

// containsMinimal reports whether set is a superset of a proven minimal
// failing set. Such sets can never be minimal themselves, regardless of
// whether they fail, so they are pruned even for non-monotone predicates.
func (s *Searcher) containsMinimal(set Set) bool {
	for _, m := range s.minimal {
		if set.Contains(m) {
			return true
		}
	}
	return false
}

// firstComb returns the smallest n-bit mask with exactly k bits set.
func firstComb(n, k int) (Set, bool) {
	if k < 0 || k > n {
		return 0, false
	}
	return Set(1<<uint(k) - 1), true
}

// nextComb advances a k-bit mask to the next mask with the same popcount in
// increasing numeric order (Gosper's hack), bounded to n bits.
func nextComb(n int, c Set) (Set, bool) {
	u := uint64(c) & -uint64(c)
	v := uint64(c) + u
	if v == 0 {
		return 0, false
	}
	next := v + (((v ^ uint64(c)) / u) >> 2)
	if next == 0 || bits.Len64(next) > n {
		return 0, false
	}
	return Set(next), true
}

// advance moves the enumeration cursor to the next candidate worth
// evaluating: feasible and not a superset of a proven minimal failing set.
// It returns false when the lattice is exhausted.
func (s *Searcher) advance() (Set, bool) {
	for {
		var cand Set
		var ok bool
		if !s.hasCursor {
			cand, ok = firstComb(s.n, s.level)
		} else {
			cand, ok = nextComb(s.n, s.cursor)
		}
		if !ok {
			s.level++
			s.hasCursor = false
			s.cursor = 0
			if s.level > s.n {
				return 0, false
			}
			continue
		}
		s.cursor = cand
		s.hasCursor = true
		if !s.Feasible(cand) || s.containsMinimal(cand) {
			continue
		}
		return cand, true
	}
}

// Run evaluates candidates until the budget is spent or the search
// completes. A negative budget means unlimited. Because candidates are
// enumerated in ascending cardinality, every proper subset of an evaluated
// candidate has already been resolved, so any failing candidate returned by
// the callback is immediately proven inclusion-minimal. This holds even
// when Fail is non-monotone.
func (s *Searcher) Run(budget int) Result {
	for !s.done && budget != 0 {
		cand, ok := s.advance()
		if !ok {
			s.done = true
			break
		}
		if s.fail(cand) {
			s.minimal = append(s.minimal, cand)
		}
		s.evals++
		if budget > 0 {
			budget--
		}
	}
	return s.snapshot()
}

func (s *Searcher) snapshot() Result {
	return Result{
		Minimal:   append([]Set(nil), s.minimal...),
		Evals:     s.evals,
		Complete:  s.done,
		Level:     s.level,
		Cursor:    s.cursor,
		HasCursor: s.hasCursor,
	}
}

// Classify reports the current Status of a single mutation set.
func (s *Searcher) Classify(set Set) Status {
	if !s.Feasible(set) {
		return StatusInfeasible
	}
	for _, m := range s.minimal {
		if set == m {
			return StatusProvenMinimal
		}
		if set.Contains(m) {
			return StatusDecided
		}
	}
	k := bits.OnesCount64(uint64(set))
	switch {
	case k < s.level:
		return StatusDecided
	case k == s.level && s.hasCursor && set <= s.cursor:
		return StatusDecided
	case k == s.level && s.done:
		return StatusDecided
	}
	return StatusUnsearched
}

// ForEachUnsearched streams every candidate in the unsearched region without
// evaluating it, in deterministic enumeration order. Returning false from fn
// stops the iteration early. The region is derived from the retained cursor
// state, so it stays consistent across budget-limited Run calls.
func (s *Searcher) ForEachUnsearched(fn func(Set) bool) {
	if s.done {
		return
	}
	probe := &Searcher{
		n:         s.n,
		conflict:  s.conflict,
		requires:  s.requires,
		minimal:   s.minimal,
		level:     s.level,
		cursor:    s.cursor,
		hasCursor: s.hasCursor,
	}
	for {
		cand, ok := probe.advance()
		if !ok {
			return
		}
		if !fn(cand) {
			return
		}
	}
}

// SortedMinimal returns the proven minimal failing sets as sorted index
// slices, convenient for assertions in the Go test host.
func (r Result) SortedMinimal() [][]int {
	out := make([][]int, len(r.Minimal))
	for i, m := range r.Minimal {
		out[i] = m.Indices()
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i]) != len(out[j]) {
			return len(out[i]) < len(out[j])
		}
		for k := range out[i] {
			if out[i][k] != out[j][k] {
				return out[i][k] < out[j][k]
			}
		}
		return false
	})
	return out
}
