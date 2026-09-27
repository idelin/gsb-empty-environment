package clocksync

import (
	"math"
	"testing"
)

func TestIntervalIntersect(t *testing.T) {
	got := Intersect(Interval{1, 5}, Interval{3, 9})
	if got != (Interval{3, 5}) {
		t.Fatalf("intersect = %v", got)
	}
	if !Intersect(Interval{1, 2}, Interval{3, 4}).Empty() {
		t.Fatal("disjoint intervals should be empty")
	}
}

func TestDeriveConstraintsKeepsNegativeBound(t *testing.T) {
	// recv < send on the wire: the bound must stay negative, never
	// be clamped to zero.
	msgs := []Message{{ID: "m1", From: "A", To: "B", SendTS: 100, RecvTS: 92}}
	cons := DeriveConstraints(msgs)
	if len(cons) != 1 {
		t.Fatalf("got %d constraints", len(cons))
	}
	if cons[0].Bound != -8 {
		t.Fatalf("bound = %v, want -8 (negative durations must not be clamped)", cons[0].Bound)
	}
}

func TestSolveConsistentRoundTrip(t *testing.T) {
	cons := []Constraint{
		{From: "A", To: "B", Bound: 57, Msg: "m1"},
		{From: "B", To: "A", Bound: -45, Msg: "m2"},
	}
	res := Solve([]string{"A", "B"}, cons)
	if res.Conflict != nil {
		t.Fatalf("unexpected conflict: %v", res.Conflict)
	}
	iv := res.Intervals["B"]
	if iv.Lo != 45 || iv.Hi != 57 {
		t.Fatalf("interval B = %v, want [45, 57]", iv)
	}
	if iv := res.Intervals["A"]; iv.Lo != 0 || iv.Hi != 0 {
		t.Fatalf("reference interval = %v, want [0, 0]", iv)
	}
	off := res.Offsets["B"] - res.Offsets["A"]
	if off < 45 || off > 57 {
		t.Fatalf("picked offset %v outside feasible interval", off)
	}
}

func TestSolveConflictReturnsChain(t *testing.T) {
	// Round trip implying theta[B]-theta[A] <= 5 and >= 10.
	cons := []Constraint{
		{From: "A", To: "B", Bound: 5, Msg: "m1"},
		{From: "B", To: "A", Bound: -10, Msg: "m2"},
	}
	res := Solve([]string{"A", "B"}, cons)
	if res.Conflict == nil {
		t.Fatal("expected conflict, got none")
	}
	if len(res.Conflict.Chain) != 2 {
		t.Fatalf("chain length = %d, want 2", len(res.Conflict.Chain))
	}
	if res.Conflict.Total >= 0 {
		t.Fatalf("cycle total = %v, want < 0", res.Conflict.Total)
	}
	seen := map[string]bool{}
	for _, c := range res.Conflict.Chain {
		seen[c.Msg] = true
	}
	if !seen["m1"] || !seen["m2"] {
		t.Fatalf("chain should reference both messages: %v", res.Conflict)
	}
}

func TestSolveThreeNodeConflictChain(t *testing.T) {
	// Cycle A->B->C->A with negative total weight.
	cons := []Constraint{
		{From: "A", To: "B", Bound: 4, Msg: "ab"},
		{From: "B", To: "C", Bound: 4, Msg: "bc"},
		{From: "C", To: "A", Bound: -12, Msg: "ca"},
	}
	res := Solve([]string{"A", "B", "C"}, cons)
	if res.Conflict == nil {
		t.Fatal("expected conflict, got none")
	}
	if len(res.Conflict.Chain) != 3 {
		t.Fatalf("chain length = %d, want 3: %v", len(res.Conflict.Chain), res.Conflict)
	}
	if res.Conflict.Total != -4 {
		t.Fatalf("cycle total = %v, want -4", res.Conflict.Total)
	}
}

func TestSolveSparseReturnsWideIntervals(t *testing.T) {
	cons := []Constraint{{From: "A", To: "B", Bound: 40, Msg: "m1"}}
	res := Solve([]string{"A", "B", "C"}, cons)
	if res.Conflict != nil {
		t.Fatalf("unexpected conflict: %v", res.Conflict)
	}
	ivB := res.Intervals["B"]
	if !math.IsInf(ivB.Lo, -1) || ivB.Hi != 40 {
		t.Fatalf("interval B = %v, want [-inf, 40]", ivB)
	}
	if ivC := res.Intervals["C"]; ivC != Wide() {
		t.Fatalf("interval C = %v, want [-inf, +inf]", ivC)
	}
}

func TestSolveTransitiveTightening(t *testing.T) {
	// Direct bound 57 on A->B, but path A->C->B gives 20+30=50.
	cons := []Constraint{
		{From: "A", To: "B", Bound: 57, Msg: "m1"},
		{From: "A", To: "C", Bound: 20, Msg: "m2"},
		{From: "C", To: "B", Bound: 30, Msg: "m3"},
	}
	res := Solve([]string{"A", "B", "C"}, cons)
	if res.Conflict != nil {
		t.Fatalf("unexpected conflict: %v", res.Conflict)
	}
	if hi := res.Intervals["B"].Hi; hi != 50 {
		t.Fatalf("hi(B) = %v, want 50 via transitive path", hi)
	}
}
