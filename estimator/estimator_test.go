package estimator

import (
	"reflect"
	"testing"
)

const U = Unknown

func TestCleanPeriodicSignal(t *testing.T) {
	res, err := Estimate(NewSignal([]int{1, 2, 3, 1, 2, 3, 1, 2, 3}), 1, 5, -1)
	if err != nil {
		t.Fatal(err)
	}
	if res.Period != 3 || res.Anomalies != 0 {
		t.Fatalf("got period=%d anomalies=%d, want 3/0", res.Period, res.Anomalies)
	}
	if !reflect.DeepEqual(res.Pattern, []int{1, 2, 3}) {
		t.Fatalf("pattern = %v", res.Pattern)
	}
	if len(res.UnknownPos) != 0 {
		t.Fatalf("unknown positions = %v, want none", res.UnknownPos)
	}
}

func TestUnknownIsNotZero(t *testing.T) {
	// If '?' were treated as 0, period 2 would need anomalies; with unknowns
	// ignored, period 2 explains everything perfectly.
	res, err := Estimate(NewSignal([]int{5, 7, U, 7, 5, U, 5, 7}), 1, 4, -1)
	if err != nil {
		t.Fatal(err)
	}
	if res.Period != 2 || res.Anomalies != 0 {
		t.Fatalf("got period=%d anomalies=%d, want 2/0", res.Period, res.Anomalies)
	}
	if !reflect.DeepEqual(res.Pattern, []int{5, 7}) {
		t.Fatalf("pattern = %v", res.Pattern)
	}
}

func TestAnomaliesAreCounted(t *testing.T) {
	// One glitch (9) in an otherwise period-2 signal. The range stops at 3
	// because period 4 would explain the glitch exactly with 0 anomalies.
	res, err := Estimate(NewSignal([]int{1, 2, 1, 9, 1, 2}), 1, 3, -1)
	if err != nil {
		t.Fatal(err)
	}
	if res.Period != 2 || res.Anomalies != 1 {
		t.Fatalf("got period=%d anomalies=%d, want 2/1", res.Period, res.Anomalies)
	}
	if !reflect.DeepEqual(res.AnomalyPos, []int{3}) {
		t.Fatalf("anomaly positions = %v", res.AnomalyPos)
	}
	if !reflect.DeepEqual(res.Pattern, []int{1, 2}) {
		t.Fatalf("pattern = %v", res.Pattern)
	}
}

func TestMaxAnomaliesFilter(t *testing.T) {
	sig := NewSignal([]int{1, 2, 1, 9, 1, 2})
	if _, err := Estimate(sig, 2, 2, 0); err == nil {
		t.Fatal("want error when no period fits within k=0")
	}
	if _, err := Estimate(sig, 2, 2, 1); err != nil {
		t.Fatalf("k=1 should succeed: %v", err)
	}
}

func TestPeriodMultipleAmbiguity(t *testing.T) {
	// Periods 2, 4, 6 all explain the signal; the fundamental period wins.
	res, err := Estimate(NewSignal([]int{1, 2, 1, 2, 1, 2}), 1, 6, -1)
	if err != nil {
		t.Fatal(err)
	}
	if res.Period != 2 {
		t.Fatalf("got period=%d, want fundamental period 2", res.Period)
	}
}

func TestFewestAnomaliesBeatsShorterPeriod(t *testing.T) {
	// Period 2 needs 2 anomalies, period 4 needs 0; anomalies dominate.
	res, err := Estimate(NewSignal([]int{1, 2, 3, 2, 1, 2, 3, 2}), 1, 4, -1)
	if err != nil {
		t.Fatal(err)
	}
	if res.Period != 4 || res.Anomalies != 0 {
		t.Fatalf("got period=%d anomalies=%d, want 4/0", res.Period, res.Anomalies)
	}
}

func TestAllUnknownSignal(t *testing.T) {
	res, err := Estimate(NewSignal([]int{U, U, U, U, U}), 2, 4, -1)
	if err != nil {
		t.Fatal(err)
	}
	if res.Period != 2 || res.Anomalies != 0 {
		t.Fatalf("got period=%d anomalies=%d, want 2/0", res.Period, res.Anomalies)
	}
	if !reflect.DeepEqual(res.UnknownPos, []int{0, 1}) {
		t.Fatalf("unknown positions = %v, want [0 1]", res.UnknownPos)
	}
	for _, v := range res.Pattern {
		if v != Unknown {
			t.Fatalf("pattern = %v, want all unknown", res.Pattern)
		}
	}
}

func TestUnknownPositionsInPattern(t *testing.T) {
	// No known sample at index 1 mod 3, so pattern slot 1 stays unknown.
	res, err := Estimate(NewSignal([]int{4, U, 6, 4, U, 6}), 3, 3, -1)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Pattern, []int{4, U, 6}) {
		t.Fatalf("pattern = %v", res.Pattern)
	}
	if !reflect.DeepEqual(res.UnknownPos, []int{1}) {
		t.Fatalf("unknown positions = %v, want [1]", res.UnknownPos)
	}
}

func TestPeriodRangeClampedToSignalLength(t *testing.T) {
	res, err := Estimate(NewSignal([]int{7, 8}), 1, 100, -1)
	if err != nil {
		t.Fatal(err)
	}
	if res.Period != 2 || res.Anomalies != 0 {
		t.Fatalf("got period=%d anomalies=%d, want 2/0", res.Period, res.Anomalies)
	}
}

func TestInvalidInput(t *testing.T) {
	if _, err := Estimate(NewSignal(nil), 1, 1, -1); err == nil {
		t.Fatal("want error for empty signal")
	}
	if _, err := Estimate(NewSignal([]int{1}), 0, 1, -1); err == nil {
		t.Fatal("want error for pMin < 1")
	}
	if _, err := Estimate(NewSignal([]int{1}), 2, 1, -1); err == nil {
		t.Fatal("want error for empty range")
	}
}
