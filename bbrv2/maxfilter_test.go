package bbrv2

import "testing"

// TestMaxFilterWindowedMax exercises the three-slot windowed max: the maximum
// is retained while within the window and expires once it ages out.
func TestMaxFilterWindowedMax(t *testing.T) {
	f := newMaxFilter(10)

	if f.get() != 0 {
		t.Fatalf("empty filter must return 0, got %d", f.get())
	}

	// A high value in round 1 becomes the running maximum and survives lower
	// samples in subsequent rounds.
	f.runningMax(1, 100)
	f.runningMax(2, 40)
	f.runningMax(3, 70)
	if f.get() != 100 {
		t.Fatalf("expected max 100 within window, got %d", f.get())
	}

	// A new higher sample replaces the maximum.
	f.runningMax(4, 150)
	if f.get() != 150 {
		t.Fatalf("expected new max 150, got %d", f.get())
	}

	// Zero samples never overwrite real measurements.
	f.runningMax(5, 0)
	if f.get() != 150 {
		t.Fatalf("zero sample must be ignored, got %d", f.get())
	}

	// Once the window ages past the round-1 high value and no equally high
	// sample refreshes it, the filter must forget it. Feed lower values until
	// the original slot expires.
	for r := uint32(6); r <= 20; r++ {
		f.runningMax(r, 50)
	}
	if f.get() != 50 {
		t.Fatalf("expected stale high value to expire, got %d", f.get())
	}
}

// TestMaxFilterReset verifies the history can be cleared and reseeded.
func TestMaxFilterReset(t *testing.T) {
	f := newMaxFilter(10)
	f.runningMax(1, 999)
	f.reset(10)
	if f.get() != 0 {
		t.Fatalf("reset must clear the filter, got %d", f.get())
	}
	f.runningMax(2, 11)
	if f.get() != 11 {
		t.Fatalf("expected reseeded max 11, got %d", f.get())
	}
}
