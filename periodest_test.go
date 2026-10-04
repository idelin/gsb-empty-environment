package periodest

import (
	"reflect"
	"testing"
)

func ints(vs ...int) []int {
	if vs == nil {
		return []int{}
	}
	return vs
}

func check(t *testing.T, got Result, err error, want Result) {
	t.Helper()
	if err != nil {
		t.Fatalf("Estimate returned error: %v", err)
	}
	if got.Period != want.Period {
		t.Errorf("Period = %d, want %d", got.Period, want.Period)
	}
	if got.Anomalies != want.Anomalies {
		t.Errorf("Anomalies = %d, want %d", got.Anomalies, want.Anomalies)
	}
	if !reflect.DeepEqual(got.Pattern, want.Pattern) {
		t.Errorf("Pattern = %v, want %v", got.Pattern, want.Pattern)
	}
	if !reflect.DeepEqual(got.UnknownPositions, want.UnknownPositions) {
		t.Errorf("UnknownPositions = %v, want %v", got.UnknownPositions, want.UnknownPositions)
	}
}

func TestExactPeriod(t *testing.T) {
	got, err := Estimate(ints(0, 1, 0, 1, 0, 1), Config{MinPeriod: 1, MaxPeriod: 6, MaxAnomalies: -1})
	check(t, got, err, Result{Period: 2, Anomalies: 0, Pattern: ints(0, 1), UnknownPositions: ints()})
}

func TestUnknownsAreNotZero(t *testing.T) {
	// If Unknown were read as 0, period 1 would score 2 anomalies and the
	// pattern would flip to 0; skipping unknowns yields pattern [1] cleanly.
	got, err := Estimate(ints(1, Unknown, 1, Unknown, 1), Config{MinPeriod: 1, MaxPeriod: 3, MaxAnomalies: -1})
	check(t, got, err, Result{Period: 1, Anomalies: 0, Pattern: ints(1), UnknownPositions: ints()})
}

func TestPeriodMultipleAmbiguity(t *testing.T) {
	// Periods 2, 4 and 8 all explain the signal with 0 anomalies; the
	// shortest (fundamental) period must win.
	got, err := Estimate(ints(1, 0, 1, 0, 1, 0, 1, 0), Config{MinPeriod: 1, MaxPeriod: 8, MaxAnomalies: -1})
	check(t, got, err, Result{Period: 2, Anomalies: 0, Pattern: ints(1, 0), UnknownPositions: ints()})
}

func TestFewestAnomaliesWins(t *testing.T) {
	// Period 1 scores 2 anomalies (four 0s vs two 1s); period 2 scores 1.
	got, err := Estimate(ints(0, 1, 0, 1, 0, 0), Config{MinPeriod: 1, MaxPeriod: 2, MaxAnomalies: -1})
	check(t, got, err, Result{Period: 2, Anomalies: 1, Pattern: ints(0, 1), UnknownPositions: ints()})
}

func TestUnknownPositionsRemainInPattern(t *testing.T) {
	// Phase 1 sees only unknown samples, so it stays unknown in the pattern.
	got, err := Estimate(ints(1, Unknown, 1, Unknown), Config{MinPeriod: 2, MaxPeriod: 2, MaxAnomalies: -1})
	check(t, got, err, Result{Period: 2, Anomalies: 0, Pattern: ints(1, Unknown), UnknownPositions: ints(1)})
}

func TestShorterPeriodAlsoResolvesUnknownPhases(t *testing.T) {
	// Period 2 would leave phase 1 unknown; period 1 explains everything
	// with the same 0 anomalies and no unknown phases, so it wins.
	got, err := Estimate(ints(1, Unknown, 1, Unknown), Config{MinPeriod: 1, MaxPeriod: 4, MaxAnomalies: -1})
	check(t, got, err, Result{Period: 1, Anomalies: 0, Pattern: ints(1), UnknownPositions: ints()})
}

func TestAllUnknownInput(t *testing.T) {
	// Every candidate scores 0 anomalies; the shortest period wins and its
	// whole pattern remains unknown.
	got, err := Estimate(ints(Unknown, Unknown, Unknown, Unknown), Config{MinPeriod: 1, MaxPeriod: 3, MaxAnomalies: -1})
	check(t, got, err, Result{Period: 1, Anomalies: 0, Pattern: ints(Unknown), UnknownPositions: ints(0)})
}

func TestEmptySignal(t *testing.T) {
	got, err := Estimate(nil, Config{MinPeriod: 2, MaxPeriod: 4, MaxAnomalies: -1})
	check(t, got, err, Result{Period: 2, Anomalies: 0, Pattern: ints(Unknown, Unknown), UnknownPositions: ints(0, 1)})
}

func TestPeriodLongerThanSignal(t *testing.T) {
	got, err := Estimate(ints(7), Config{MinPeriod: 3, MaxPeriod: 3, MaxAnomalies: -1})
	check(t, got, err, Result{Period: 3, Anomalies: 0, Pattern: ints(7, Unknown, Unknown), UnknownPositions: ints(1, 2)})
}

func TestMaxAnomaliesCap(t *testing.T) {
	signal := ints(0, 1, 0, 1, 0, 0) // best period 2 needs 1 anomaly
	if _, err := Estimate(signal, Config{MinPeriod: 1, MaxPeriod: 2, MaxAnomalies: 0}); err != ErrNoSolution {
		t.Fatalf("err = %v, want ErrNoSolution", err)
	}
	got, err := Estimate(signal, Config{MinPeriod: 1, MaxPeriod: 2, MaxAnomalies: 1})
	check(t, got, err, Result{Period: 2, Anomalies: 1, Pattern: ints(0, 1), UnknownPositions: ints()})
}

func TestMajorityTieBreaksToSmallestValue(t *testing.T) {
	got, err := Estimate(ints(2, 1), Config{MinPeriod: 1, MaxPeriod: 1, MaxAnomalies: -1})
	check(t, got, err, Result{Period: 1, Anomalies: 1, Pattern: ints(1), UnknownPositions: ints()})
}

func TestInvalidConfig(t *testing.T) {
	if _, err := Estimate(ints(1), Config{MinPeriod: 0, MaxPeriod: 2, MaxAnomalies: -1}); err == nil {
		t.Error("MinPeriod 0: expected error, got nil")
	}
	if _, err := Estimate(ints(1), Config{MinPeriod: 3, MaxPeriod: 2, MaxAnomalies: -1}); err == nil {
		t.Error("MaxPeriod < MinPeriod: expected error, got nil")
	}
	if _, err := Estimate(ints(1, -2), Config{MinPeriod: 1, MaxPeriod: 2, MaxAnomalies: -1}); err == nil {
		t.Error("negative sample other than Unknown: expected error, got nil")
	}
}
