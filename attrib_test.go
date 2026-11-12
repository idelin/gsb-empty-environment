package netsec

import (
	"math/rand"
	"reflect"
	"testing"
)

// bruteForceMinimal enumerates the whole feasible lattice and returns every
// inclusion-minimal failing set, used as the reference oracle.
func bruteForceMinimal(t *testing.T, p Problem) [][]int {
	t.Helper()
	s, err := NewSearcher(Problem{N: p.N, Conflicts: p.Conflicts, Requires: p.Requires, Fail: func(Set) bool { return false }})
	if err != nil {
		t.Fatalf("NewSearcher: %v", err)
	}
	var failing []Set
	for mask := Set(0); ; mask++ {
		if s.Feasible(mask) && p.Fail(mask) {
			failing = append(failing, mask)
		}
		if p.N < 64 && mask == Set(1)<<uint(p.N)-1 {
			break
		}
	}
	isMinimal := func(candidate Set) bool {
		for _, other := range failing {
			if other != candidate && candidate.Contains(other) {
				return false
			}
		}
		return true
	}
	var out [][]int
	for _, f := range failing {
		if isMinimal(f) {
			out = append(out, f.Indices())
		}
	}
	return out
}

func equalSets(a, b [][]int) bool {
	if len(a) != len(b) {
		return false
	}
	count := make(map[[64]int]int)
	encode := func(s []int) [64]int {
		var arr [64]int
		copy(arr[:], s)
		for i := len(s); i < 64; i++ {
			arr[i] = -1
		}
		return arr
	}
	for _, s := range a {
		count[encode(s)]++
	}
	for _, s := range b {
		count[encode(s)]--
	}
	for _, c := range count {
		if c != 0 {
			return false
		}
	}
	return true
}

func TestMonotoneBaseline(t *testing.T) {
	p := Problem{
		N:    5,
		Fail: func(s Set) bool { return s.Contains(SetOf(1, 3)) },
	}
	s, err := NewSearcher(p)
	if err != nil {
		t.Fatalf("NewSearcher: %v", err)
	}
	res := s.Run(-1)
	if !res.Complete {
		t.Fatal("search did not complete with unlimited budget")
	}
	want := [][]int{{1, 3}}
	if got := res.SortedMinimal(); !reflect.DeepEqual(got, want) {
		t.Fatalf("minimal = %v, want %v", got, want)
	}
}

// TestNonMonotoneDeletionDoesNotEliminateFailure pins the central hazard:
// with a non-monotone predicate, deleting mutations from a failing set does
// NOT reliably clear the fault, and extending a passing set can trigger one.
// The parity predicate below fails iff an odd number of mutations 0..2 are
// active, so {0} fails, {0,1} passes, and {0,1,2} fails again.
func TestNonMonotoneDeletionDoesNotEliminateFailure(t *testing.T) {
	parity := func(s Set) bool {
		n := 0
		for _, i := range s.Indices() {
			if i < 3 {
				n++
			}
		}
		return n%2 == 1
	}
	// The non-monotone witnesses: deletion re-introduces the fault.
	if !parity(SetOf(0, 1, 2)) {
		t.Fatal("precondition: {0,1,2} must fail")
	}
	if parity(SetOf(0, 1)) {
		t.Fatal("precondition: deleting mutation 2 clears the fault")
	}
	if !parity(SetOf(0)) {
		t.Fatal("precondition: deleting mutation 1 re-introduces the fault")
	}
	// A monotone-assuming pruner would discard supersets of the passing
	// set {0,1} and never discover that {0,1,2} fails.
	if !parity(SetOf(0, 1, 2)) || parity(SetOf(0, 1)) {
		t.Fatal("precondition: passing set has a failing superset")
	}

	p := Problem{N: 4, Fail: parity}
	s, err := NewSearcher(p)
	if err != nil {
		t.Fatalf("NewSearcher: %v", err)
	}
	res := s.Run(-1)
	want := [][]int{{0}, {1}, {2}}
	if got := res.SortedMinimal(); !reflect.DeepEqual(got, want) {
		t.Fatalf("minimal = %v, want %v", got, want)
	}
	// {0,1,2} fails but is not minimal: it contains proven singletons.
	if st := s.Classify(SetOf(0, 1, 2)); st != StatusDecided {
		t.Fatalf("Classify({0,1,2}) = %v, want %v", st, StatusDecided)
	}
	if got := bruteForceMinimal(t, p); !equalSets(got, want) {
		t.Fatalf("brute force oracle disagrees: %v", got)
	}
}

func TestConstraintsRespected(t *testing.T) {
	p := Problem{
		N:         6,
		Conflicts: [][2]int{{0, 1}, {2, 3}},
		Requires:  [][2]int{{4, 0}, {5, 4}}, // 5 -> 4 -> 0 transitively
		Fail: func(s Set) bool {
			return s.Contains(SetOf(0, 2)) || s.Contains(SetOf(1, 4))
		},
	}
	var queried []Set
	wrapped := p
	wrapped.Fail = func(s Set) bool {
		queried = append(queried, s)
		return p.Fail(s)
	}
	s, err := NewSearcher(wrapped)
	if err != nil {
		t.Fatalf("NewSearcher: %v", err)
	}
	res := s.Run(-1)
	for _, q := range queried {
		if !s.Feasible(q) {
			t.Fatalf("callback received infeasible set %v", q.Indices())
		}
	}
	// {0,2} is feasible and failing; {1,4} is infeasible (4 requires 0,
	// and 0 conflicts with 1), so it must never surface.
	want := [][]int{{0, 2}}
	if got := res.SortedMinimal(); !reflect.DeepEqual(got, want) {
		t.Fatalf("minimal = %v, want %v", got, want)
	}
	if got := bruteForceMinimal(t, p); !reflect.DeepEqual(got, want) {
		t.Fatalf("brute force oracle = %v, want %v", got, want)
	}
	if s.Feasible(SetOf(0, 1)) {
		t.Fatal("{0,1} must be infeasible (conflict)")
	}
	if s.Feasible(SetOf(5, 4)) {
		t.Fatal("{5,4} must be infeasible (5 transitively requires 0)")
	}
	if !s.Feasible(SetOf(5, 4, 0)) {
		t.Fatal("{5,4,0} must be feasible (dependency closed)")
	}
}

// TestBruteForceCrossCheck randomizes non-monotone truth tables plus
// exclusion/dependency relations and compares against the oracle.
func TestBruteForceCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(20261112))
	for trial := 0; trial < 40; trial++ {
		n := 2 + rng.Intn(6) // 2..7 mutations
		table := make(map[Set]bool)
		for mask := Set(0); mask < Set(1)<<uint(n); mask++ {
			table[mask] = rng.Intn(4) == 0 // 25% failing, non-monotone
		}
		p := Problem{
			N:    n,
			Fail: func(s Set) bool { return table[s] },
		}
		for i := 0; i < n; i++ {
			for j := i + 1; j < n; j++ {
				switch rng.Intn(12) {
				case 0:
					p.Conflicts = append(p.Conflicts, [2]int{i, j})
				case 1:
					p.Requires = append(p.Requires, [2]int{i, j})
				}
			}
		}
		s, err := NewSearcher(p)
		if err != nil {
			t.Fatalf("trial %d: NewSearcher: %v", trial, err)
		}
		res := s.Run(-1)
		if !res.Complete {
			t.Fatalf("trial %d: incomplete with unlimited budget", trial)
		}
		if got, want := res.SortedMinimal(), bruteForceMinimal(t, p); !equalSets(got, want) {
			t.Fatalf("trial %d: minimal = %v, oracle = %v", trial, got, want)
		}
	}
}

// TestBudgetExhaustionRetainsProgress verifies that a drained budget keeps
// every proven minimal set, keeps the unsearched region coherent, and that
// resuming with fresh budget converges to the unlimited-budget answer.
func TestBudgetExhaustionRetainsProgress(t *testing.T) {
	parity := func(s Set) bool {
		n := 0
		for _, i := range s.Indices() {
			if i < 5 {
				n++
			}
		}
		return n%2 == 1
	}
	p := Problem{N: 6, Fail: parity}
	s, err := NewSearcher(p)
	if err != nil {
		t.Fatalf("NewSearcher: %v", err)
	}

	res := s.Run(3) // empty set + two singletons at most
	if res.Complete {
		t.Fatal("search should not complete within budget 3")
	}
	if res.Evals != 3 {
		t.Fatalf("Evals = %d, want 3", res.Evals)
	}
	// Every retained set must be genuinely minimal (provable from the
	// fully explored lower levels alone).
	for _, m := range res.Minimal {
		indices := m.Indices()
		if len(indices) != 1 || indices[0] >= 5 {
			t.Fatalf("retained set %v is not a proven minimal failure", indices)
		}
	}
	// The unsearched region must be non-empty and disjoint from decided
	// sets; together they must cover every feasible candidate.
	unsearched := map[Set]bool{}
	s.ForEachUnsearched(func(c Set) bool {
		unsearched[c] = true
		return true
	})
	if len(unsearched) == 0 {
		t.Fatal("unsearched region must survive budget exhaustion")
	}
	for c := range unsearched {
		if st := s.Classify(c); st != StatusUnsearched {
			t.Fatalf("ForEachUnsearched yielded %v but Classify says %v", c.Indices(), st)
		}
	}
	for mask := Set(0); mask < Set(1)<<uint(p.N); mask++ {
		st := s.Classify(mask)
		_, inRegion := unsearched[mask]
		switch {
		case st == StatusUnsearched && !inRegion:
			t.Fatalf("set %v classified unsearched but missing from region", mask.Indices())
		case st != StatusUnsearched && inRegion:
			t.Fatalf("set %v in region but classified %v", mask.Indices(), st)
		}
	}

	// Resuming with more budget must reach exactly the unlimited answer.
	for !res.Complete {
		res = s.Run(7)
	}
	want := [][]int{{0}, {1}, {2}, {3}, {4}}
	if got := res.SortedMinimal(); !reflect.DeepEqual(got, want) {
		t.Fatalf("minimal = %v, want %v", got, want)
	}
	fresh, err := NewSearcher(p)
	if err != nil {
		t.Fatalf("NewSearcher: %v", err)
	}
	if got := fresh.Run(-1).SortedMinimal(); !reflect.DeepEqual(got, want) {
		t.Fatalf("unlimited run = %v, want %v", got, want)
	}
}

func TestEmptySetFailureDominates(t *testing.T) {
	p := Problem{N: 4, Fail: func(s Set) bool { return true }}
	s, err := NewSearcher(p)
	if err != nil {
		t.Fatalf("NewSearcher: %v", err)
	}
	res := s.Run(-1)
	want := [][]int{{}}
	if got := res.SortedMinimal(); !reflect.DeepEqual(got, want) {
		t.Fatalf("minimal = %v, want %v", got, want)
	}
	if res.Evals != 1 {
		t.Fatalf("Evals = %d, want 1 (everything above the empty set is pruned)", res.Evals)
	}
}

func TestDeterministicEnumeration(t *testing.T) {
	table := map[Set]bool{SetOf(0, 2): true, SetOf(1): true, SetOf(0, 1, 3): true}
	p := Problem{N: 4, Fail: func(s Set) bool { return table[s] }}
	run := func() Result {
		s, err := NewSearcher(p)
		if err != nil {
			t.Fatalf("NewSearcher: %v", err)
		}
		return s.Run(-1)
	}
	first, second := run(), run()
	if !reflect.DeepEqual(first.Minimal, second.Minimal) {
		t.Fatalf("non-deterministic enumeration: %v vs %v", first.Minimal, second.Minimal)
	}
	if first.Evals != second.Evals {
		t.Fatalf("non-deterministic eval count: %d vs %d", first.Evals, second.Evals)
	}
}

func TestValidation(t *testing.T) {
	if _, err := NewSearcher(Problem{N: 65, Fail: func(Set) bool { return false }}); err == nil {
		t.Fatal("expected error for N > 64")
	}
	if _, err := NewSearcher(Problem{N: 2}); err == nil {
		t.Fatal("expected error for nil Fail")
	}
	if _, err := NewSearcher(Problem{N: 2, Conflicts: [][2]int{{0, 5}}, Fail: func(Set) bool { return false }}); err == nil {
		t.Fatal("expected error for out-of-range conflict index")
	}
}
