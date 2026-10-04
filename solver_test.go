package main

import (
	"math/rand"
	"testing"
)

func pts(list ...[2]int) []Point {
	out := make([]Point, len(list))
	for i, p := range list {
		out[i] = Point{X: p[0], Y: p[1]}
	}
	return out
}

func solve(t *testing.T, in Input) Result {
	t.Helper()
	res := Solve(in, Options{})
	if !res.Feasible {
		t.Fatalf("expected feasible, got uncoverable=%v", res.Uncoverable)
	}
	return res
}

// Adjacent targets with no forbidden point merge into one solid rectangle:
// area tie with singles, but fewer rectangles wins the second objective.
func TestSolidMergeMinCount(t *testing.T) {
	res := solve(t, Input{Targets: pts([2]int{0, 0}, [2]int{1, 0})})
	if res.TotalArea != 2 || res.RectCount != 1 {
		t.Fatalf("got area=%d count=%d, want 2/1", res.TotalArea, res.RectCount)
	}
	if res.Rects[0].Rect != (Rect{0, 0, 1, 0}) {
		t.Fatalf("unexpected rect %+v", res.Rects[0].Rect)
	}
}

// A forbidden point between targets blocks the merged rectangle.
func TestForbiddenBlocksMerge(t *testing.T) {
	res := solve(t, Input{
		Targets:   pts([2]int{0, 0}, [2]int{2, 0}),
		Forbidden: pts([2]int{1, 0}),
	})
	if res.TotalArea != 2 || res.RectCount != 2 {
		t.Fatalf("got area=%d count=%d, want 2/2", res.TotalArea, res.RectCount)
	}
	for _, r := range res.Rects {
		if r.Rect.Contains(Point{1, 0}) {
			t.Fatalf("rect %+v contains forbidden point", r.Rect)
		}
	}
}

// A 2x2 target block is one rectangle of area 4.
func TestBlock2x2(t *testing.T) {
	res := solve(t, Input{Targets: pts(
		[2]int{0, 0}, [2]int{1, 0}, [2]int{0, 1}, [2]int{1, 1})})
	if res.TotalArea != 4 || res.RectCount != 1 {
		t.Fatalf("got area=%d count=%d, want 4/1", res.TotalArea, res.RectCount)
	}
}

// Two solid runs separated by a gap: minimum area is 4, minimum count is 2.
func TestSecondObjectiveCount(t *testing.T) {
	res := solve(t, Input{Targets: pts(
		[2]int{0, 0}, [2]int{1, 0}, [2]int{3, 0}, [2]int{4, 0})})
	if res.TotalArea != 4 || res.RectCount != 2 {
		t.Fatalf("got area=%d count=%d, want 4/2", res.TotalArea, res.RectCount)
	}
}

// Gap: a target that is also forbidden can never be covered.
func TestUncoverableForbiddenTarget(t *testing.T) {
	res := Solve(Input{
		Targets:   pts([2]int{0, 0}, [2]int{1, 1}),
		Forbidden: pts([2]int{1, 1}),
	}, Options{})
	if res.Feasible {
		t.Fatalf("expected infeasible")
	}
	if len(res.Uncoverable) != 1 || res.Uncoverable[0] != (Point{1, 1}) {
		t.Fatalf("uncoverable=%v, want [(1,1)]", res.Uncoverable)
	}
}

// Overlap is allowed but double-charged: reported total area must equal the
// sum of individual rectangle areas, and the optimum never exceeds the
// all-singles cost (area == #targets).
func TestOverlapDoubleChargedInvariant(t *testing.T) {
	res := solve(t, Input{Targets: pts(
		[2]int{0, 0}, [2]int{2, 0}, [2]int{1, 1})})
	var sum int64
	for _, r := range res.Rects {
		sum += r.Area
	}
	if sum != res.TotalArea {
		t.Fatalf("totalArea=%d but rect areas sum to %d", res.TotalArea, sum)
	}
	if res.TotalArea != 3 {
		t.Fatalf("area=%d, want 3 (one cell per target)", res.TotalArea)
	}
}

// Evidence: every target is covered by exactly the rectangles listed.
func TestEvidenceComplete(t *testing.T) {
	in := Input{
		Targets:   pts([2]int{0, 0}, [2]int{1, 0}, [2]int{0, 1}, [2]int{3, 3}),
		Forbidden: pts([2]int{1, 1}),
	}
	res := solve(t, in)
	if len(res.Evidence) != len(in.Targets) {
		t.Fatalf("evidence len=%d", len(res.Evidence))
	}
	for ti, ev := range res.Evidence {
		if len(ev.Rects) == 0 {
			t.Fatalf("target %d %v has no covering rect", ti, ev.Target)
		}
		for _, ri := range ev.Rects {
			if !res.Rects[ri].Rect.Contains(ev.Target) {
				t.Fatalf("evidence says rect %d covers %v but it does not", ri, ev.Target)
			}
		}
	}
	// and conversely RectSol.Covers must agree with Evidence
	for ri, r := range res.Rects {
		for _, ti := range r.Covers {
			found := false
			for _, x := range res.Evidence[ti].Rects {
				if x == ri {
					found = true
				}
			}
			if !found {
				t.Fatalf("rect %d claims target %d, evidence disagrees", ri, ti)
			}
		}
	}
}

// Budget interruption: a hard instance with a tiny node budget must stop and
// report Interrupted; with unlimited budget it must solve to the same optimum
// as the DP path would (cross-checked against brute force here).
func TestBudgetInterrupt(t *testing.T) {
	var targets []Point
	for x := 0; x < 6; x++ {
		for y := 0; y < 5; y++ {
			if (x+y)%2 == 0 {
				targets = append(targets, Point{x, y})
			}
		}
	}
	in := Input{Targets: targets}  // 15 targets, but force B&B via dp-budget 0
	forced := Options{DPBudget: 1} // DP only for <=1 target -> B&B path

	limited := forced
	limited.MaxNodes = 1
	res := Solve(in, limited)
	if !res.Interrupted {
		t.Fatalf("expected interrupted with MaxNodes=1")
	}

	full := Solve(in, forced)
	if full.Interrupted || !full.Feasible {
		t.Fatalf("unlimited B&B failed: %+v", full)
	}
	if full.TotalArea != int64(len(targets)) {
		t.Fatalf("B&B area=%d, want %d", full.TotalArea, len(targets))
	}
}

// Small-grid cross-check: solver result must equal brute force over ALL
// candidate subsets (gap / isolated-point / budget logic included).
func TestBruteForceCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	checked := 0
	for trial := 0; trial < 400 && checked < 60; trial++ {
		w := 2 + rng.Intn(3) // 2..4
		h := 2 + rng.Intn(3)
		var targets, forbidden []Point
		for x := 0; x < w; x++ {
			for y := 0; y < h; y++ {
				switch rng.Intn(5) {
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
		in := Input{Targets: targets, Forbidden: forbidden}
		best, err := BruteForce(in, Options{})
		if err != nil {
			continue // too many candidates for exhaustive subsets; skip
		}
		res := Solve(in, Options{})
		if !res.Feasible {
			t.Fatalf("trial %d: infeasible but brute force found %v", trial, best)
		}
		if res.TotalArea != best.Area || res.RectCount != best.Count {
			t.Fatalf("trial %d: solver=(%d,%d) brute=(%d,%d) in=%+v",
				trial, res.TotalArea, res.RectCount, best.Area, best.Count, in)
		}
		checked++
	}
	if checked < 20 {
		t.Fatalf("only %d cases cross-checked", checked)
	}
}

// Same cross-check but through the B&B path (tiny DP budget).
func TestBnBCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	checked := 0
	for trial := 0; trial < 400 && checked < 40; trial++ {
		w := 2 + rng.Intn(3)
		h := 2 + rng.Intn(3)
		var targets, forbidden []Point
		for x := 0; x < w; x++ {
			for y := 0; y < h; y++ {
				switch rng.Intn(4) {
				case 0:
					targets = append(targets, Point{x, y})
				case 1:
					forbidden = append(forbidden, Point{x, y})
				}
			}
		}
		if len(targets) < 2 {
			continue
		}
		in := Input{Targets: targets, Forbidden: forbidden}
		best, err := BruteForce(in, Options{})
		if err != nil {
			continue
		}
		res := Solve(in, Options{DPBudget: 1, MaxNodes: 5_000_000})
		if !res.Feasible || res.Interrupted {
			t.Fatalf("trial %d: B&B failed feasible=%v interrupted=%v", trial, res.Feasible, res.Interrupted)
		}
		if res.TotalArea != best.Area || res.RectCount != best.Count {
			t.Fatalf("trial %d: bnb=(%d,%d) brute=(%d,%d) in=%+v",
				trial, res.TotalArea, res.RectCount, best.Area, best.Count, in)
		}
		checked++
	}
	if checked < 15 {
		t.Fatalf("only %d cases cross-checked", checked)
	}
}

// Optimal area always equals the number of (coverable) targets: each
// rectangle has area >= #targets inside it, and singletons achieve equality.
func TestAreaEqualsTargetCount(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	for trial := 0; trial < 50; trial++ {
		w := 3 + rng.Intn(4)
		h := 3 + rng.Intn(4)
		var targets, forbidden []Point
		for x := 0; x < w; x++ {
			for y := 0; y < h; y++ {
				switch rng.Intn(4) {
				case 0:
					targets = append(targets, Point{x, y})
				case 1:
					forbidden = append(forbidden, Point{x, y})
				}
			}
		}
		if len(targets) == 0 {
			continue
		}
		res := Solve(Input{Targets: targets, Forbidden: forbidden}, Options{})
		if !res.Feasible {
			continue
		}
		if res.TotalArea != int64(len(targets)) {
			t.Fatalf("trial %d: area=%d targets=%d", trial, res.TotalArea, len(targets))
		}
	}
}

// Duplicate input points must not change the result.
func TestDuplicatePoints(t *testing.T) {
	a := solve(t, Input{Targets: pts([2]int{0, 0}, [2]int{1, 0})})
	b := solve(t, Input{Targets: pts([2]int{0, 0}, [2]int{0, 0}, [2]int{1, 0})})
	if a.TotalArea != b.TotalArea || a.RectCount != b.RectCount {
		t.Fatalf("duplicates changed result: %+v vs %+v", a, b)
	}
}

// Empty target set is trivially feasible with zero cost.
func TestEmptyTargets(t *testing.T) {
	res := Solve(Input{}, Options{})
	if !res.Feasible || res.TotalArea != 0 || res.RectCount != 0 {
		t.Fatalf("got %+v", res)
	}
}
