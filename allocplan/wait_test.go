package allocplan

import (
	"errors"
	"testing"
)

// TestWaitThenRetry drives the full host workflow: an exhausted allocation
// reports the earliest retry tick, the host advances the virtual clock to
// that exact tick, and the same request then succeeds without weakening
// quarantine.
func TestWaitThenRetry(t *testing.T) {
	planner, _ := New(Config{TagDomainSize: 1, AddressSpace: 4, Quarantine: 6})

	start, tag, err := planner.Alloc(2)
	if err != nil || tag != 1 {
		t.Fatalf("initial Alloc = (%d,%d,%v)", start, tag, err)
	}
	if err := planner.Free(start); err != nil {
		t.Fatalf("Free: %v", err)
	}

	hint, waitable := planner.NextAvailableTick(2)
	if !waitable || hint != 6 {
		t.Fatalf("NextAvailableTick = (%d,%v), want (6,true)", hint, waitable)
	}
	if _, _, err := planner.Alloc(2); !errors.Is(err, ErrRejected) {
		t.Fatalf("immediate retry error = %v, want rejection", err)
	}

	planner.Clock().AdvanceTo(hint)
	retryStart, retryTag, err := planner.Alloc(2)
	if err != nil || retryStart != 0 || retryTag != 1 {
		t.Fatalf("retry after wait = (%d,%d,%v), want tag 1 at 0", retryStart, retryTag, err)
	}
	if planner.Clock().Tick() != 6 {
		t.Fatalf("clock = %d, want 6", planner.Clock().Tick())
	}
}

// TestAllTagsBusyIsNotWaitable confirms that when every tag has a live
// reference the rejection is explicitly non-waitable.
func TestAllTagsBusyIsNotWaitable(t *testing.T) {
	planner, _ := New(Config{TagDomainSize: 1, AddressSpace: 8, Quarantine: 2})
	if _, _, err := planner.Alloc(2); err != nil {
		t.Fatalf("first Alloc: %v", err)
	}
	_, _, err := planner.Alloc(2)
	if !errors.Is(err, ErrRejected) || !errors.Is(err, ErrAllTagsBusy) {
		t.Fatalf("second Alloc error = %v, want non-waitable ErrAllTagsBusy", err)
	}
	var exhausted *ExhaustedError
	if !errors.As(err, &exhausted) || exhausted.Waitable {
		t.Fatalf("error = %v, want non-waitable ExhaustedError", err)
	}
	if _, waitable := planner.NextAvailableTick(2); waitable {
		t.Fatal("NextAvailableTick must be false while all tags are live")
	}
}
