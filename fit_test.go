package beatfit

import (
	"math"
	"testing"
)

var params = Params{MissPenalty: 2, ExtraPenalty: 2}

func almostEq(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// Extras at +1 after every beat make adjacent diffs alternate 1,9,1,9,...;
// the diff mode is never 10, but the true grid has period 10.
func TestDiffModeWouldFail(t *testing.T) {
	times := []float64{0, 1, 10, 11, 20, 21, 30}
	res := FitAll(times, IntegerCandidates(2, 20), params)
	if res.Periods[0] != 10 || len(res.Periods) != 1 {
		t.Fatalf("want unique period 10, got %v (cost %v)", res.Periods, res.BestCost)
	}
	sol := res.Solutions[0]
	if len(sol.Matches) != 4 || len(sol.ExtraTaps) != 3 || len(sol.MissedBeats) != 0 {
		t.Fatalf("want 4 matches / 3 extras / 0 misses, got %+v", sol)
	}
	if !almostEq(sol.Cost, 3*params.ExtraPenalty) {
		t.Fatalf("want cost 6, got %v", sol.Cost)
	}
	for _, mt := range sol.Matches {
		if !almostEq(mt.GridTime, mt.TapTime) {
			t.Fatalf("matched tap off grid: %+v", mt)
		}
	}
}

// A missing tap at t=10 must be reported as a missed beat, not as period 20.
func TestMissedBeat(t *testing.T) {
	times := []float64{0, 20, 30}
	res := FitAll(times, IntegerCandidates(2, 30), params)
	if res.Periods[0] != 10 {
		t.Fatalf("want period 10, got %v", res.Periods)
	}
	sol := res.Solutions[0]
	if len(sol.MissedBeats) != 1 || sol.MissedBeats[0] != 1 {
		t.Fatalf("want missed beat k=1 (t=10), got %+v", sol.MissedBeats)
	}
	if !almostEq(sol.Cost, params.MissPenalty) {
		t.Fatalf("want cost 2, got %v", sol.Cost)
	}
}

// Order preservation: taps must map to strictly increasing beats.
func TestOrderPreserved(t *testing.T) {
	times := []float64{0.2, 4.8, 9.7, 14.9, 15.4, 20.1}
	res := FitAll(times, IntegerCandidates(2, 25), params)
	sol := res.Solutions[0]
	prev := math.MinInt
	for _, mt := range sol.Matches {
		if mt.Beat <= prev {
			t.Fatalf("beats not strictly increasing: %+v", sol.Matches)
		}
		prev = mt.Beat
	}
}

// Noisy taps: deviations are charged, grid still wins.
func TestNoisyMatchCost(t *testing.T) {
	times := []float64{0.3, 9.8, 20.2, 29.9}
	res := FitAll(times, IntegerCandidates(2, 40), params)
	if res.Periods[0] != 10 {
		t.Fatalf("want period 10, got %v", res.Periods)
	}
	sol := res.Solutions[0]
	if !almostEq(sol.Offset, 0) {
		t.Fatalf("want offset 0, got %v", sol.Offset)
	}
	want := 0.3 + 0.2 + 0.2 + 0.1
	if !almostEq(sol.Cost, want) {
		t.Fatalf("want cost %v, got %v", want, sol.Cost)
	}
}

// Single tap: many grids hit it exactly -> all their periods must be
// reported as tied.
func TestTiedPeriodsAllReported(t *testing.T) {
	res := FitAll([]float64{5}, IntegerCandidates(1, 10), Params{MissPenalty: 1, ExtraPenalty: 1})
	want := []int{3, 4, 5, 6, 7, 8, 9, 10}
	if len(res.Periods) != len(want) {
		t.Fatalf("want periods %v, got %v", want, res.Periods)
	}
	for i, p := range want {
		if res.Periods[i] != p {
			t.Fatalf("want periods %v, got %v", want, res.Periods)
		}
	}
	if !almostEq(res.BestCost, 0) {
		t.Fatalf("want cost 0, got %v", res.BestCost)
	}
}

// With zero miss penalty, period 3 and 6 both fit {0,6} at cost 0.
func TestTiedPeriodsZeroMiss(t *testing.T) {
	res := FitAll([]float64{0, 6}, IntegerCandidates(3, 6), Params{MissPenalty: 0, ExtraPenalty: 2})
	if len(res.Periods) != 2 || res.Periods[0] != 3 || res.Periods[1] != 6 {
		t.Fatalf("want periods [3 6], got %v", res.Periods)
	}
}

func TestUnsortedAndEmpty(t *testing.T) {
	res := FitAll([]float64{20, 0, 10}, IntegerCandidates(5, 15), params)
	if res.Periods[0] != 10 || !almostEq(res.BestCost, 0) {
		t.Fatalf("unsorted input: got %v cost %v", res.Periods, res.BestCost)
	}
	res = FitAll(nil, IntegerCandidates(1, 5), params)
	if !almostEq(res.BestCost, 0) {
		t.Fatalf("empty input: want cost 0, got %v", res.BestCost)
	}
}

// Grid points farther than miss+extra beyond the tap range are pruned.
func TestFarPointsSkipped(t *testing.T) {
	pr := Params{MissPenalty: 1, ExtraPenalty: 1}
	sol := FitCandidate([]float64{0}, Candidate{Period: 10, Offset: 0}, pr)
	if len(sol.Matches) != 1 || len(sol.MissedBeats) != 0 || !almostEq(sol.Cost, 0) {
		t.Fatalf("want single exact match, got %+v", sol)
	}
	// Interior beats cannot be skipped for free: matching both taps and
	// paying 9 misses (cost 9) beats dropping one tap (cost 11).
	sol = FitCandidate([]float64{0, 100}, Candidate{Period: 10, Offset: 0}, pr)
	if len(sol.Matches) != 2 || len(sol.MissedBeats) != 9 || !almostEq(sol.Cost, 9) {
		t.Fatalf("want 2 matches + 9 misses, got %+v", sol)
	}
}
