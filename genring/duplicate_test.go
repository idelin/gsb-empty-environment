package genring

import (
	"errors"
	"testing"
)

func TestDuplicateCompletionDoesNotReleaseTwice(t *testing.T) {
	// Keep the duplicate slot occupied by completing it out of order: the
	// older slot stays in "completed" state until the newer one drains.
	r, rc := newTestRing(t, 4, 3, 2)
	idx0, gen0, _ := r.Submit(1, 1)
	idx1, gen1, _ := r.Submit(2, 2)

	// Complete the older slot while the newer one keeps the tail blocked, so
	// slot 0 stays occupied in completed state instead of being reclaimed.
	if _, err := r.Complete(idx0, r.Tag(gen0)); err != nil {
		t.Fatalf("older complete: %v", err)
	}
	res, err := r.Complete(idx0, r.Tag(gen0))
	if !errors.Is(err, ErrDuplicateCompletion) || !res.Duplicate || res.Released {
		t.Fatalf("duplicate = %+v, %v", res, err)
	}
	// Slot 0 stays occupied behind the in-flight slot 1, so no lease has been
	// released yet, but the duplicate still must not release anything.
	if r.Stats().Duplicates != 1 || r.Stats().Released != 0 {
		t.Fatalf("counters after duplicate = %+v", r.Stats())
	}
	if !r.slots[idx0].completed {
		t.Fatal("slot 0 should remain completed but occupied")
	}

	// Releasing the newer slot drains and reclaims both.
	if _, err := r.Complete(idx1, r.Tag(gen1)); err != nil {
		t.Fatalf("newer complete: %v", err)
	}
	// After reclaim the same CQE is late, not a duplicate, and still harmless.
	res, err = r.Complete(idx0, r.Tag(gen0))
	if !errors.Is(err, ErrStaleCompletion) || res.Released {
		t.Fatalf("late after reclaim = %+v, %v", res, err)
	}

	if rc.counts[1] != 1 || rc.counts[2] != 1 {
		t.Fatalf("lease release counts = %+v", rc.counts)
	}
}

func TestLateCompletionAfterSlotReuseProtectsNewTenant(t *testing.T) {
	r, rc := newTestRing(t, 4, 3, 2)
	idx0, gen0, _ := r.Submit(1, 1)
	idx1, gen1, _ := r.Submit(2, 2)

	// Drain both, then reuse slot 0 for a different tenant.
	if _, err := r.Complete(idx0, r.Tag(gen0)); err != nil {
		t.Fatalf("complete 0: %v", err)
	}
	if _, err := r.Complete(idx1, r.Tag(gen1)); err != nil {
		t.Fatalf("complete 1: %v", err)
	}
	newIdx, newGen, err := r.Submit(3, 3)
	if err != nil {
		t.Fatalf("reuse submit: %v", err)
	}

	// The stale CQE from the previous occupant must never touch lease 3.
	res, err := r.Complete(idx0, r.Tag(gen0))
	if !errors.Is(err, ErrStaleCompletion) || !res.Stale || res.Released {
		t.Fatalf("late completion = %+v, %v", res, err)
	}
	if _, err := r.Complete(newIdx, r.Tag(newGen)); err != nil {
		t.Fatalf("new-tenant complete: %v", err)
	}

	if rc.counts[1] != 1 || rc.counts[2] != 1 || rc.counts[3] != 1 {
		t.Fatalf("lease release counts = %+v", rc.counts)
	}
	if r.Stats().Stale != 1 {
		t.Fatalf("stale count = %d, want 1", r.Stats().Stale)
	}
}

func TestSlotMismatchQuarantines(t *testing.T) {
	r, rc := newTestRing(t, 4, 3, 2)
	idx0, gen0, _ := r.Submit(1, 1)
	_, _, _ = r.Submit(2, 2)

	// Report slot 1 using slot 0's generation: unique owner, wrong index.
	res, err := r.Complete(1, r.Tag(gen0))
	if !errors.Is(err, ErrSlotMismatch) || !res.Quarantined {
		t.Fatalf("mismatch = %+v, %v", res, err)
	}
	if !r.Isolated() {
		t.Fatal("ring should be isolated")
	}
	if _, err := r.Complete(idx0, r.Tag(gen0)); !errors.Is(err, ErrIsolated) {
		t.Fatalf("post-quarantine complete err = %v", err)
	}
	if _, _, err := r.Submit(3, 3); !errors.Is(err, ErrIsolated) {
		t.Fatalf("post-quarantine submit err = %v", err)
	}
	// Both in-flight leases are recovered exactly once.
	if rc.counts[1] != 1 || rc.counts[2] != 1 {
		t.Fatalf("release counts = %+v", rc.counts)
	}
}
