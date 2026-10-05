package rectcover

import (
	"math/rand"
	"testing"
)

func mustVerify(t *testing.T, p Problem, sol Solution) {
	t.Helper()
	if err := p.Verify(sol); err != nil {
		t.Fatalf("verify failed: %v (solution %+v)", err, sol)
	}
}

func TestSingleTarget(t *testing.T) {
	p := Problem{Targets: []Point{{3, 4}}}
	sol := Solve(p, Options{})
	if sol.Status != StatusOptimal {
		t.Fatalf("status = %v", sol.Status)
	}
	if sol.Area != 1 || len(sol.Rects) != 1 {
		t.Fatalf("area=%d rects=%d, want 1/1", sol.Area, len(sol.Rects))
	}
	if sol.Rects[0] != (Rect{3, 4, 3, 4}) {
		t.Fatalf("rect = %+v", sol.Rects[0])
	}
	mustVerify(t, p, sol)
}

func TestRowOfTargets(t *testing.T) {
	p := Problem{Targets: []Point{{0, 0}, {1, 0}, {2, 0}}}
	sol := Solve(p, Options{})
	if sol.Area != 3 || len(sol.Rects) != 1 {
		t.Fatalf("area=%d rects=%d, want 3/1", sol.Area, len(sol.Rects))
	}
	mustVerify(t, p, sol)
}

// A forbidden point between two targets forces a split: no single rectangle
// can cover both, so the optimum is two 1x1 rectangles.
func TestForbiddenSplitsRow(t *testing.T) {
	p := Problem{
		Targets:   []Point{{0, 0}, {2, 0}},
		Forbidden: []Point{{1, 0}},
	}
	sol := Solve(p, Options{})
	if sol.Status != StatusOptimal {
		t.Fatalf("status = %v", sol.Status)
	}
	if sol.Area != 2 || len(sol.Rects) != 2 {
		t.Fatalf("area=%d rects=%d, want 2/2", sol.Area, len(sol.Rects))
	}
	mustVerify(t, p, sol)
}

// A forbidden center blocks any rectangle spanning opposite corners; the
// optimum falls back to four 1x1 rectangles (area 4), beating two strips
// (area 6) and the full 3x3 square (area 9, and infeasible anyway).
func TestForbiddenCenterGap(t *testing.T) {
	p := Problem{
		Targets:   []Point{{0, 0}, {2, 0}, {0, 2}, {2, 2}},
		Forbidden: []Point{{1, 1}},
	}
	sol := Solve(p, Options{})
	if sol.Area != 4 || len(sol.Rects) != 4 {
		t.Fatalf("area=%d rects=%d, want 4/4", sol.Area, len(sol.Rects))
	}
	mustVerify(t, p, sol)
}

// A target ringed by forbidden points is isolated: its only candidate is the
// 1x1 rectangle on itself, which the solver must pick.
func TestIsolatedPoint(t *testing.T) {
	p := Problem{
		Targets: []Point{{1, 1}, {5, 5}},
		Forbidden: []Point{
			{0, 1}, {2, 1}, {1, 0}, {1, 2},
		},
	}
	sol := Solve(p, Options{})
	if sol.Status != StatusOptimal {
		t.Fatalf("status = %v", sol.Status)
	}
	if sol.Area != 2 || len(sol.Rects) != 2 {
		t.Fatalf("area=%d rects=%d, want 2/2", sol.Area, len(sol.Rects))
	}
	foundUnit := false
	for _, r := range sol.Rects {
		if r == (Rect{1, 1, 1, 1}) {
			foundUnit = true
		}
	}
	if !foundUnit {
		t.Fatalf("isolated target not covered by its 1x1 rectangle: %+v", sol.Rects)
	}
	mustVerify(t, p, sol)
}

func TestTargetForbiddenConflict(t *testing.T) {
	p := Problem{
		Targets:   []Point{{1, 1}},
		Forbidden: []Point{{1, 1}},
	}
	if sol := Solve(p, Options{}); sol.Status != StatusInfeasible {
		t.Fatalf("status = %v, want infeasible", sol.Status)
	}
	if _, err := SolveExhaustive(p); err != nil {
		t.Fatalf("exhaustive err = %v", err)
	} else if sol := mustExhaustive(t, p); sol.Status != StatusInfeasible {
		t.Fatalf("exhaustive status = %v, want infeasible", sol.Status)
	}
}

func mustExhaustive(t *testing.T, p Problem) Solution {
	t.Helper()
	sol, err := SolveExhaustive(p)
	if err != nil {
		t.Fatalf("SolveExhaustive: %v", err)
	}
	return sol
}

// A node budget of 1 interrupts the search before any leaf is reached.
func TestNodeBudgetInterrupt(t *testing.T) {
	p := Problem{Targets: []Point{{0, 0}, {1, 0}, {2, 0}}}
	sol := Solve(p, Options{MaxNodes: 1})
	if sol.Status != StatusBudgetExhausted {
		t.Fatalf("status = %v, want budget_exhausted", sol.Status)
	}
	if sol.Rects != nil {
		t.Fatalf("interrupted before any leaf, want no solution, got %+v", sol.Rects)
	}
}

// An area budget below the optimum makes the instance infeasible within
// budget; a budget at the optimum keeps it solvable.
func TestAreaBudget(t *testing.T) {
	p := Problem{
		Targets:   []Point{{0, 0}, {2, 0}},
		Forbidden: []Point{{1, 0}},
	}
	if sol := Solve(p, Options{MaxArea: 1}); sol.Status != StatusInfeasible {
		t.Fatalf("MaxArea=1: status = %v, want infeasible", sol.Status)
	}
	if sol := Solve(p, Options{MaxArea: 2}); sol.Status != StatusOptimal || sol.Area != 2 {
		t.Fatalf("MaxArea=2: status=%v area=%d", sol.Status, sol.Area)
	}
}

func TestRectBudget(t *testing.T) {
	p := Problem{
		Targets:   []Point{{0, 0}, {2, 0}},
		Forbidden: []Point{{1, 0}},
	}
	if sol := Solve(p, Options{MaxRects: 1}); sol.Status != StatusInfeasible {
		t.Fatalf("MaxRects=1: status = %v, want infeasible", sol.Status)
	}
	if sol := Solve(p, Options{MaxRects: 2}); sol.Status != StatusOptimal {
		t.Fatalf("MaxRects=2: status = %v", sol.Status)
	}
}

// Overlapping rectangles are allowed and their overlap is charged twice.
func TestOverlapChargedRepeatedly(t *testing.T) {
	p := Problem{Targets: []Point{{0, 0}, {2, 0}}}
	sol := Solution{
		Status: StatusOptimal,
		Rects: []Rect{
			{0, 0, 1, 0}, // area 2
			{1, 0, 2, 0}, // area 2, overlaps the first at (1,0)
		},
		Area: 4,
	}
	sol.Evidence = buildEvidence(p.Targets, sol.Rects)
	if err := p.Verify(sol); err != nil {
		t.Fatalf("overlapping cover rejected: %v", err)
	}
}

// Area dominates count: three 1x1 rectangles (area 3) lose to one 1x3
// rectangle (area 3? no) -- here a single 2x2 block (area 4) must beat four
// 1x1 rectangles only when area ties; use a case where fewer rectangles cost
// more area to confirm area wins the lexicographic order.
func TestAreaBeatsCount(t *testing.T) {
	// Targets (0,0) and (1,1) with forbidden (1,0) and (0,1): the only
	// cover is two 1x1 rectangles, area 2. Add a third target so that one
	// 1x3 rectangle (area 3, count 1) competes with splits.
	p := Problem{
		Targets:   []Point{{0, 0}, {1, 0}, {2, 0}},
		Forbidden: []Point{{1, 1}},
	}
	sol := Solve(p, Options{})
	// One 1x3 strip has area 3; three 1x1 rectangles also area 3 but count
	// 3, so the strip must win on the count tie-break.
	if sol.Area != 3 || len(sol.Rects) != 1 {
		t.Fatalf("area=%d rects=%d, want 3/1", sol.Area, len(sol.Rects))
	}
	mustVerify(t, p, sol)
}

func TestEvidenceCoversEveryTarget(t *testing.T) {
	p := Problem{
		Targets:   []Point{{0, 0}, {2, 0}, {0, 2}, {2, 2}, {5, 5}},
		Forbidden: []Point{{1, 1}},
	}
	sol := Solve(p, Options{})
	if len(sol.Evidence) != len(p.Targets) {
		t.Fatalf("evidence len = %d", len(sol.Evidence))
	}
	for _, e := range sol.Evidence {
		if e.Rect < 0 || !sol.Rects[e.Rect].Contains(e.Target) {
			t.Fatalf("bad evidence for %v", e.Target)
		}
	}
	mustVerify(t, p, sol)
}

// Cross-check the branch-and-bound solver against exhaustive subset
// enumeration on random small grids, including gaps and isolated points.
// Trials whose candidate count exceeds the exhaustive cap are skipped.
func TestCrossCheckExhaustive(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	checked := 0
	for trial := 0; trial < 600 && checked < 150; trial++ {
		w := rng.Intn(4) + 1
		h := rng.Intn(4) + 1
		var targets, forbidden []Point
		for x := 0; x < w; x++ {
			for y := 0; y < h; y++ {
				switch rng.Intn(4) {
				case 0, 1:
					targets = append(targets, Point{x, y})
				case 2:
					forbidden = append(forbidden, Point{x, y})
				}
			}
		}
		if len(targets) == 0 {
			continue
		}
		p := Problem{Targets: targets, Forbidden: forbidden}
		got := Solve(p, Options{})
		want, err := SolveExhaustive(p)
		if err == ErrTooManyCandidates {
			continue
		}
		if err != nil {
			t.Fatalf("SolveExhaustive: %v", err)
		}
		checked++
		if got.Status != want.Status {
			t.Fatalf("trial %d: status %v vs %v (problem %+v)", trial, got.Status, want.Status, p)
		}
		if got.Status != StatusOptimal {
			continue
		}
		if got.Area != want.Area || len(got.Rects) != len(want.Rects) {
			t.Fatalf("trial %d: got area=%d rects=%d, want area=%d rects=%d (problem %+v)",
				trial, got.Area, len(got.Rects), want.Area, len(want.Rects), p)
		}
		mustVerify(t, p, got)
	}
}

// Duplicate targets and forbidden points must not change the result.
func TestDuplicateInputs(t *testing.T) {
	p := Problem{
		Targets:   []Point{{0, 0}, {0, 0}, {2, 0}, {2, 0}},
		Forbidden: []Point{{1, 0}, {1, 0}},
	}
	sol := Solve(p, Options{})
	if sol.Area != 2 || len(sol.Rects) != 2 {
		t.Fatalf("area=%d rects=%d, want 2/2", sol.Area, len(sol.Rects))
	}
	mustVerify(t, p, sol)
}

func TestEmptyProblem(t *testing.T) {
	sol := Solve(Problem{}, Options{})
	if sol.Status != StatusOptimal || sol.Area != 0 || len(sol.Rects) != 0 {
		t.Fatalf("empty problem: %+v", sol)
	}
}
