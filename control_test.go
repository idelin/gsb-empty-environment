package netsec

import (
	"context"
	"errors"
	"testing"
)

func TestDefaultClassifier(t *testing.T) {
	if class, timedOut := DefaultClassifier(nil); class != "" || timedOut {
		t.Fatalf("nil error classified as %q/%t", class, timedOut)
	}
	if class, timedOut := DefaultClassifier(TimeoutError); class != "timeout" || !timedOut {
		t.Fatalf("TimeoutError classified as %q/%t", class, timedOut)
	}
	if class, timedOut := DefaultClassifier(context.DeadlineExceeded); class != "timeout" || !timedOut {
		t.Fatalf("DeadlineExceeded classified as %q/%t", class, timedOut)
	}
	want := errors.New("boom")
	if class, timedOut := DefaultClassifier(want); class != "boom" || timedOut {
		t.Fatalf("plain error classified as %q/%t", class, timedOut)
	}
}

// TestDefaultOptions exercises zero-value Options: cascade policy and the
// default classifier, with no logger or context supplied.
func TestDefaultOptions(t *testing.T) {
	harness := newFakeHarness(fakeConfig{})
	result, err := Minimize(harness, fullTrace(), Options{})
	if err != nil {
		t.Fatalf("Minimize with zero Options failed: %v", err)
	}
	assertMinimal(t, harness, result)
	if result.TargetFailure == "" {
		t.Fatalf("missing target failure class")
	}
}

func TestRepairWithoutFunc(t *testing.T) {
	harness := newFakeHarness(fakeConfig{})
	if _, err := Minimize(harness, fullTrace(), Options{Policy: Repair}); err == nil {
		t.Fatalf("Repair policy without a RepairFunc was accepted")
	}
}

// TestDeterministicAcrossRuns runs the same reduction repeatedly and requires
// identical traces, proving map iteration order does not leak into results.
func TestDeterministicAcrossRuns(t *testing.T) {
	var want []int
	for i := 0; i < 5; i++ {
		harness := newFakeHarness(fakeConfig{})
		result := MinimizeT(t, harness, fullTrace(), Options{Classify: classify})
		got := messageIDs(result.Messages)
		if want == nil {
			want = got
			continue
		}
		if len(got) != len(want) {
			t.Fatalf("run %d length %d, want %d (%v vs %v)", i, len(got), len(want), got, want)
		}
		for j := range got {
			if got[j] != want[j] {
				t.Fatalf("run %d ids %v, want %v", i, got, want)
			}
		}
	}
}

// TestMaxRuns limits candidate sessions and surfaces an error rather than an
// unverified fixpoint when the budget is too small.
func TestMaxRuns(t *testing.T) {
	harness := newFakeHarness(fakeConfig{})
	_, err := Minimize(harness, fullTrace(), Options{
		Classify: classify,
		MaxRuns:  2,
	})
	if err == nil {
		t.Fatalf("Minimize accepted an unverified result under a tiny MaxRuns")
	}
}

// TestContextCancel propagates caller cancellation into reduction.
func TestContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	harness := newFakeHarness(fakeConfig{})
	_, err := Minimize(harness, fullTrace(), Options{
		Context:  ctx,
		Classify: classify,
	})
	if err == nil {
		t.Fatalf("Minimize ignored a canceled context")
	}
}
