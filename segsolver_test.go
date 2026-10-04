package segsolver

import (
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// bruteForce enumerates every legal segmentation of the sequence and
// returns the minimum cost together with the lexicographically smallest
// optimal cut list. It is the independent reference used to recompute
// expectations for small sequences.
func bruteForce(n, minLen, maxLen int, cost []int64, must, forbid map[int]bool) (int64, []int, bool) {
	var best int64
	var bestCuts []int
	ok := false
	var rec func(pos int, acc int64, cuts []int)
	rec = func(pos int, acc int64, cuts []int) {
		if pos == n {
			if !ok || acc < best || (acc == best && lexLess(cuts, bestCuts)) {
				best, ok = acc, true
				bestCuts = append([]int(nil), cuts...)
			}
			return
		}
		for l := minLen; l <= maxLen && pos+l <= n; l++ {
			end := pos + l
			legal := true
			for p := pos + 1; p < end; p++ {
				if must[p] {
					legal = false
					break
				}
			}
			if !legal || (end < n && forbid[end]) {
				continue
			}
			next := cuts
			if end < n {
				next = append(append([]int(nil), cuts...), end)
			}
			rec(end, acc+cost[l], next)
		}
	}
	rec(0, 0, nil)
	return best, bestCuts, ok
}

// lexLess compares ascending cut lists; a proper prefix is smaller.
func lexLess(a, b []int) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

func sortedKeys(m map[int]bool) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

// cutsEqual compares cut lists element-wise, treating nil and empty as
// equal.
func cutsEqual(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestAgainstBruteForce recomputes every expectation by full enumeration
// on small random sequences, including negative segment costs and dense
// mandatory/forbidden position sets.
func TestAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(20261005))
	for iter := 0; iter < 4000; iter++ {
		n := rng.Intn(10) // 0..9
		minLen := 1 + rng.Intn(3)
		maxLen := minLen + rng.Intn(3)
		cost := make([]int64, maxLen+1)
		for l := minLen; l <= maxLen; l++ {
			cost[l] = int64(rng.Intn(15) - 5) // [-5, 9], negatives included
		}
		must := map[int]bool{}
		forbid := map[int]bool{}
		for p := 1; p < n; p++ {
			switch rng.Intn(6) {
			case 0:
				must[p] = true
			case 1:
				forbid[p] = true
			}
		}

		s, err := New(n, minLen, maxLen, cost, sortedKeys(must), sortedKeys(forbid))
		if err != nil {
			t.Fatalf("iter %d: unexpected New error: %v", iter, err)
		}
		got := s.Solve()
		wantVal, wantCuts, wantOK := bruteForce(n, minLen, maxLen, cost, must, forbid)

		if got.Feasible != wantOK {
			t.Fatalf("iter %d: feasibility mismatch: got %v want %v (n=%d lens=[%d,%d] cost=%v must=%v forbid=%v)",
				iter, got.Feasible, wantOK, n, minLen, maxLen, cost, must, forbid)
		}
		if !wantOK {
			continue
		}
		if got.Value != wantVal {
			t.Fatalf("iter %d: value mismatch: got %d want %d (n=%d lens=[%d,%d] cost=%v must=%v forbid=%v)",
				iter, got.Value, wantVal, n, minLen, maxLen, cost, must, forbid)
		}
		if !cutsEqual(got.Cuts, wantCuts) {
			t.Fatalf("iter %d: cuts mismatch: got %v want %v (n=%d lens=[%d,%d] cost=%v must=%v forbid=%v)",
				iter, got.Cuts, wantCuts, n, minLen, maxLen, cost, must, forbid)
		}

		// The returned cuts must be valid evidence for the value.
		checked, err := s.Check(got.Cuts)
		if err != nil {
			t.Fatalf("iter %d: evidence rejected: %v", iter, err)
		}
		if checked != got.Value {
			t.Fatalf("iter %d: evidence cost %d != value %d", iter, checked, got.Value)
		}

		// Budget exactly at the optimum is enough; one less is not, and
		// the legal upper bound (cheapest legal cut) must be retained.
		if r := s.SolveWithBudget(wantVal); !r.WithinBudget || !r.Feasible {
			t.Fatalf("iter %d: budget %d should suffice", iter, wantVal)
		}
		under := s.SolveWithBudget(wantVal - 1)
		if under.WithinBudget {
			t.Fatalf("iter %d: budget %d should be insufficient", iter, wantVal-1)
		}
		if !under.Feasible || under.Value != wantVal || !cutsEqual(under.Cuts, wantCuts) {
			t.Fatalf("iter %d: legal upper bound lost under tight budget: %+v", iter, under)
		}
	}
}

// TestConflictingPositions verifies that a position which is both
// mandatory and forbidden is rejected, and recomputes by enumeration
// that no legal segmentation could exist in that case.
func TestConflictingPositions(t *testing.T) {
	cost := make([]int64, 4)
	_, err := New(6, 1, 3, cost, []int{2, 4}, []int{4})
	if err == nil {
		t.Fatal("expected conflict error")
	}
	if !strings.Contains(err.Error(), "4") {
		t.Fatalf("error should name the conflicting position, got: %v", err)
	}
	// Enumeration agrees: cutting at 4 is simultaneously required and
	// forbidden, so zero legal segmentations exist.
	if _, _, ok := bruteForce(6, 1, 3, cost, map[int]bool{2: true, 4: true}, map[int]bool{4: true}); ok {
		t.Fatal("brute force found a segmentation despite the conflict")
	}
}

// TestNegativeSegmentCosts checks a hand-computed instance where the
// optimum uses negative-cost unit segments.
func TestNegativeSegmentCosts(t *testing.T) {
	cost := []int64{0, -2, 1} // length 1 costs -2, length 2 costs 1
	s, err := New(4, 1, 2, cost, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := s.Solve()
	if !got.Feasible || got.Value != -8 || !reflect.DeepEqual(got.Cuts, []int{1, 2, 3}) {
		t.Fatalf("got %+v, want value -8 with cuts [1 2 3]", got)
	}
}

// TestLexicographicTieBreak checks tie handling, including the rule that
// a cut list which is a prefix of another is lexicographically smaller.
func TestLexicographicTieBreak(t *testing.T) {
	cost := []int64{0, 0, 0, 0}

	// n=2: segmentations [] and [1] both cost 0; [] wins as a prefix.
	s, err := New(2, 1, 2, cost, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Solve(); got.Value != 0 || len(got.Cuts) != 0 {
		t.Fatalf("got %+v, want value 0 with no cuts", got)
	}

	// n=3, maxLen=2: candidates [1], [2], [1 2]; [1] is the lex-min.
	s, err = New(3, 1, 2, cost, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Solve(); got.Value != 0 || !reflect.DeepEqual(got.Cuts, []int{1}) {
		t.Fatalf("got %+v, want value 0 with cuts [1]", got)
	}
}

// TestMandatoryAndForbidden checks constraint interaction on a fixed
// instance against the enumerated answer.
func TestMandatoryAndForbidden(t *testing.T) {
	cost := []int64{0, 3, 1, 4, 2}
	must, forbid := []int{3}, []int{1, 5}
	s, err := New(6, 1, 3, cost, must, forbid)
	if err != nil {
		t.Fatal(err)
	}
	got := s.Solve()
	wantVal, wantCuts, ok := bruteForce(6, 1, 3, cost, map[int]bool{3: true}, map[int]bool{1: true, 5: true})
	if !ok {
		t.Fatal("brute force says infeasible")
	}
	if !got.Feasible || got.Value != wantVal || !cutsEqual(got.Cuts, wantCuts) {
		t.Fatalf("got %+v, want value %d cuts %v", got, wantVal, wantCuts)
	}
	for _, p := range must {
		if i := sort.SearchInts(got.Cuts, p); i >= len(got.Cuts) || got.Cuts[i] != p {
			t.Fatalf("mandatory cut %d missing from %v", p, got.Cuts)
		}
	}
	for _, p := range forbid {
		if i := sort.SearchInts(got.Cuts, p); i < len(got.Cuts) && got.Cuts[i] == p {
			t.Fatalf("forbidden cut %d present in %v", p, got.Cuts)
		}
	}
}

// TestInfeasible covers length-bound and mandatory-cut infeasibility.
func TestInfeasible(t *testing.T) {
	cost := []int64{0, 0, 1}

	// 5 cannot be tiled by segments of length exactly 2.
	s, err := New(5, 2, 2, cost, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Solve(); got.Feasible {
		t.Fatalf("expected infeasible, got %+v", got)
	}

	// A mandatory cut at 1 forces a unit segment, violating minLen=2.
	s, err = New(4, 2, 2, cost, []int{1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Solve(); got.Feasible {
		t.Fatalf("expected infeasible, got %+v", got)
	}

	// All internal positions forbidden: only the whole sequence works,
	// and it is too long here.
	s, err = New(4, 1, 2, cost, nil, []int{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Solve(); got.Feasible {
		t.Fatalf("expected infeasible, got %+v", got)
	}
}

// TestRevise checks local cost-table revision: after updating a few
// entries the solver must agree with a freshly built instance.
func TestRevise(t *testing.T) {
	cost := []int64{0, 5, 1}
	s, err := New(4, 1, 2, cost, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	before := s.Solve()
	if before.Value != 2 || !reflect.DeepEqual(before.Cuts, []int{2}) {
		t.Fatalf("before revise: got %+v, want value 2 cuts [2]", before)
	}

	if err := s.Revise(map[int]int64{1: -3}); err != nil {
		t.Fatal(err)
	}
	after := s.Solve()
	if after.Value != -12 || !reflect.DeepEqual(after.Cuts, []int{1, 2, 3}) {
		t.Fatalf("after revise: got %+v, want value -12 cuts [1 2 3]", after)
	}

	cost[1] = -3
	fresh, err := New(4, 1, 2, cost, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := fresh.Solve(); !reflect.DeepEqual(after, want) {
		t.Fatalf("revised %+v != fresh %+v", after, want)
	}

	if err := s.Revise(map[int]int64{3: 7}); err == nil {
		t.Fatal("expected out-of-range revision to fail")
	}
}

// TestBudgetUpperBound checks the insufficient-budget path explicitly:
// the cheapest legal segmentation is retained as a legal upper bound.
func TestBudgetUpperBound(t *testing.T) {
	cost := []int64{0, 2, 5}
	s, err := New(3, 1, 2, cost, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Optimum: 1+1+1 = 6 with cuts [1 2] (2+1=7, 1+2=7).
	r := s.SolveWithBudget(5)
	if !r.Feasible || r.WithinBudget {
		t.Fatalf("got %+v, want feasible but over budget", r)
	}
	if r.Value != 6 || !reflect.DeepEqual(r.Cuts, []int{1, 2}) {
		t.Fatalf("upper bound mismatch: %+v", r)
	}
	if v, err := s.Check(r.Cuts); err != nil || v != 6 {
		t.Fatalf("upper bound evidence invalid: %d, %v", v, err)
	}

	r = s.SolveWithBudget(6)
	if !r.WithinBudget {
		t.Fatalf("budget 6 should suffice: %+v", r)
	}
}

// TestCheckRejectsBadEvidence feeds illegal cut lists to Check.
func TestCheckRejectsBadEvidence(t *testing.T) {
	cost := []int64{0, 1, 1, 1}
	s, err := New(4, 1, 3, cost, []int{2}, []int{3})
	if err != nil {
		t.Fatal(err)
	}
	cases := [][]int{
		{1, 1},    // not increasing
		{4},       // out of range
		{2, 3},    // forbidden position
		{1},       // missing mandatory cut at 2
		{1, 2, 3}, // forbidden position present
	}
	for _, cuts := range cases {
		if _, err := s.Check(cuts); err == nil {
			t.Fatalf("Check(%v) should fail", cuts)
		}
	}
	if v, err := s.Check([]int{2}); err != nil || v != 2 {
		t.Fatalf("Check([2]) = %d, %v; want 2, nil", v, err)
	}
}

// TestEmptySequence covers the degenerate n=0 case.
func TestEmptySequence(t *testing.T) {
	s, err := New(0, 1, 2, []int64{0, 7, 7}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := s.Solve()
	if !got.Feasible || got.Value != 0 || len(got.Cuts) != 0 {
		t.Fatalf("got %+v, want feasible empty result", got)
	}
}
