package main

import "testing"

func mustSolve(t *testing.T, in FitInput) Solution {
	t.Helper()
	sol, err := Solve(in)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	return sol
}

func TestPerfectGrid(t *testing.T) {
	sol := mustSolve(t, FitInput{
		Taps:            []int64{0, 100, 200, 300},
		Periods:         []int64{50, 100, 150},
		SearchAllStarts: true,
		MissPenalty:     10,
		ExtraPenalty:    10,
	})
	if sol.BestCost != 0 {
		t.Fatalf("cost = %d, want 0", sol.BestCost)
	}
	if sol.Best.Period != 100 {
		t.Fatalf("period = %d, want 100", sol.Best.Period)
	}
	if len(sol.Best.Matches) != 4 || len(sol.Best.MissedBeats) != 0 || len(sol.Best.ExtraTaps) != 0 {
		t.Fatalf("unexpected alignment: %+v", sol.Best)
	}
}

func TestMissedBeat(t *testing.T) {
	// Tap missing at t=200: one interior grid point unmatched.
	sol := mustSolve(t, FitInput{
		Taps:            []int64{0, 100, 300},
		Periods:         []int64{100},
		SearchAllStarts: true,
		MissPenalty:     30,
		ExtraPenalty:    40, // matching the last tap is cheaper than dropping it
	})
	if sol.BestCost != 30 {
		t.Fatalf("cost = %d, want 30 (one missed beat)", sol.BestCost)
	}
	if len(sol.Best.MissedBeats) != 1 || sol.Best.MissedBeats[0] != 200 {
		t.Fatalf("missed beats = %v, want [200]", sol.Best.MissedBeats)
	}
	if len(sol.Best.Matches) != 3 {
		t.Fatalf("matches = %d, want 3", len(sol.Best.Matches))
	}
}

func TestMissedBeatVsExtraTradeoff(t *testing.T) {
	// Same series, but dropping the last tap (extra=25) beats paying miss=30.
	sol := mustSolve(t, FitInput{
		Taps:            []int64{0, 100, 300},
		Periods:         []int64{100},
		SearchAllStarts: true,
		MissPenalty:     30,
		ExtraPenalty:    25,
	})
	if sol.BestCost != 25 {
		t.Fatalf("cost = %d, want 25 (drop last tap)", sol.BestCost)
	}
	if len(sol.Best.ExtraTaps) != 1 || sol.Best.ExtraTaps[0].TapTime != 300 {
		t.Fatalf("extra taps = %+v, want tap at 300", sol.Best.ExtraTaps)
	}
	if len(sol.Best.MissedBeats) != 0 {
		t.Fatalf("missed beats = %v, want none", sol.Best.MissedBeats)
	}
}

func TestExtraTap(t *testing.T) {
	sol := mustSolve(t, FitInput{
		Taps:            []int64{0, 50, 100, 200},
		Periods:         []int64{100},
		SearchAllStarts: true,
		MissPenalty:     30,
		ExtraPenalty:    20,
	})
	if sol.BestCost != 20 {
		t.Fatalf("cost = %d, want 20 (one extra tap)", sol.BestCost)
	}
	if len(sol.Best.ExtraTaps) != 1 || sol.Best.ExtraTaps[0].TapTime != 50 {
		t.Fatalf("extra taps = %+v, want tap at 50", sol.Best.ExtraTaps)
	}
}

func TestDeviationCost(t *testing.T) {
	// Single tap 3 off the grid: pay deviation 3.
	sol := mustSolve(t, FitInput{
		Taps:         []int64{3},
		Periods:      []int64{10},
		Starts:       []int64{0},
		MissPenalty:  100,
		ExtraPenalty: 100,
	})
	if sol.BestCost != 3 {
		t.Fatalf("cost = %d, want 3", sol.BestCost)
	}
	// But if the extra penalty is cheaper, leave it unmatched.
	sol = mustSolve(t, FitInput{
		Taps:         []int64{3},
		Periods:      []int64{10},
		Starts:       []int64{0},
		MissPenalty:  100,
		ExtraPenalty: 2,
	})
	if sol.BestCost != 2 || len(sol.Best.Matches) != 0 {
		t.Fatalf("cost = %d matches = %d, want 2 and 0", sol.BestCost, len(sol.Best.Matches))
	}
}

func TestOrderPreserved(t *testing.T) {
	sol := mustSolve(t, FitInput{
		Taps:            []int64{0, 5, 103, 150, 210, 400},
		Periods:         []int64{100},
		SearchAllStarts: true,
		MissPenalty:     15,
		ExtraPenalty:    12,
	})
	prevTap, prevK := -1, int64(-1<<62)
	for _, m := range sol.Best.Matches {
		if m.TapIndex <= prevTap || m.GridK <= prevK {
			t.Fatalf("matching not order-preserving: %+v", sol.Best.Matches)
		}
		prevTap, prevK = m.TapIndex, m.GridK
	}
}

func TestNotJustModeOfDiffs(t *testing.T) {
	// Adjacent diffs are 200,100,300,300: their mode (300) is NOT the best
	// period. With cheap missed beats the true period 100 wins globally.
	sol := mustSolve(t, FitInput{
		Taps:            []int64{0, 200, 300, 600, 900},
		Periods:         []int64{50, 100, 150, 200, 300},
		SearchAllStarts: true,
		MissPenalty:     10,
		ExtraPenalty:    100,
	})
	if sol.Best.Period != 100 {
		t.Fatalf("period = %d, want 100 (mode of diffs would give 300)", sol.Best.Period)
	}
	// Five interior beats missing: 100, 400, 500, 700, 800.
	if sol.BestCost != 50 {
		t.Fatalf("cost = %d, want 50", sol.BestCost)
	}
	if len(sol.Best.MissedBeats) != 5 {
		t.Fatalf("missed beats = %v, want 5", sol.Best.MissedBeats)
	}
}

func TestTiedPeriods(t *testing.T) {
	// One tap at 10: periods 20, 30, 40 all match exactly with start 10.
	sol := mustSolve(t, FitInput{
		Taps:            []int64{10},
		Periods:         []int64{20, 30, 40},
		SearchAllStarts: true,
		MissPenalty:     5,
		ExtraPenalty:    5,
	})
	if sol.BestCost != 0 {
		t.Fatalf("cost = %d, want 0", sol.BestCost)
	}
	if len(sol.TiedPeriods) != 3 {
		t.Fatalf("tied periods = %+v, want 3", sol.TiedPeriods)
	}
	for i, want := range []int64{20, 30, 40} {
		if sol.TiedPeriods[i].Period != want {
			t.Fatalf("tied period[%d] = %d, want %d", i, sol.TiedPeriods[i].Period, want)
		}
		if len(sol.TiedPeriods[i].Starts) != 1 || sol.TiedPeriods[i].Starts[0] != 10 {
			t.Fatalf("starts for period %d = %v, want [10]", want, sol.TiedPeriods[i].Starts)
		}
	}
	if sol.Best.Period != 20 {
		t.Fatalf("best period = %d, want smallest tied period 20", sol.Best.Period)
	}
}

func TestUnsortedAndDuplicateTaps(t *testing.T) {
	sol := mustSolve(t, FitInput{
		Taps:            []int64{200, 0, 100, 100},
		Periods:         []int64{100},
		SearchAllStarts: true,
		MissPenalty:     10,
		ExtraPenalty:    7,
	})
	// Grid 0,100,200 matches three taps; the duplicate at 100 is extra.
	if sol.BestCost != 7 {
		t.Fatalf("cost = %d, want 7", sol.BestCost)
	}
	if len(sol.Best.ExtraTaps) != 1 || sol.Best.ExtraTaps[0].TapTime != 100 {
		t.Fatalf("extra taps = %+v, want one tap at 100", sol.Best.ExtraTaps)
	}
}

func TestEmptyTaps(t *testing.T) {
	sol := mustSolve(t, FitInput{
		Taps:            nil,
		Periods:         []int64{4, 6},
		SearchAllStarts: true,
		MissPenalty:     3,
		ExtraPenalty:    3,
	})
	if sol.BestCost != 0 || len(sol.TiedPeriods) != 2 {
		t.Fatalf("empty taps: cost = %d ties = %+v", sol.BestCost, sol.TiedPeriods)
	}
}

func TestValidation(t *testing.T) {
	if _, err := Solve(FitInput{Taps: []int64{1}, MissPenalty: 1}); err == nil {
		t.Fatal("expected error for missing periods")
	}
	if _, err := Solve(FitInput{Taps: []int64{1}, Periods: []int64{0}}); err == nil {
		t.Fatal("expected error for non-positive period")
	}
	if _, err := Solve(FitInput{Taps: []int64{1}, Periods: []int64{2}, MissPenalty: -1}); err == nil {
		t.Fatal("expected error for negative penalty")
	}
}
