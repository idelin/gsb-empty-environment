package allocplan

import (
	"errors"
	"testing"
)

// TestSameTickExpiryDecision pins down the boundary rule: a quarantine
// expiring at tick T is still blocking at T-1 and is reusable exactly at T.
// The clock is virtual, so both sides of the same tick are exercised
// deterministically.
func TestSameTickExpiryDecision(t *testing.T) {
	planner, _ := New(Config{TagDomainSize: 1, AddressSpace: 4, Quarantine: 3})

	start, tag, err := planner.Alloc(2)
	if err != nil || start != 0 || tag != 1 {
		t.Fatalf("initial Alloc = (%d,%d,%v), want (0,1,nil)", start, tag, err)
	}
	if err := planner.Free(0); err != nil {
		t.Fatalf("Free: %v", err)
	}
	until, ok := planner.QuarantineUntil(0, 1)
	if !ok || until != 3 {
		t.Fatalf("quarantine expiry = %d (ok=%v), want 3", until, ok)
	}

	planner.Clock().Advance(2)
	if planner.Eligible(1, 0, 2) {
		t.Fatal("tag eligible at tick 2, quarantine still open until 3")
	}
	if _, _, err := planner.Alloc(2); !errors.Is(err, ErrRejected) {
		t.Fatalf("alloc before expiry error = %v, want rejection", err)
	} else {
		var exhausted *ExhaustedError
		if !errors.As(err, &exhausted) || !exhausted.Waitable || exhausted.WaitTick != 3 {
			t.Fatalf("rejection = %v, want waitable hint tick 3", err)
		}
	}
	hint, waitable := planner.NextAvailableTick(2)
	if !waitable || hint != 3 {
		t.Fatalf("NextAvailableTick = (%d,%v), want (3,true)", hint, waitable)
	}

	planner.Clock().Advance(1) // tick 3: quarantine expiry tick itself
	if !planner.Eligible(1, 0, 2) {
		t.Fatal("tag still ineligible on the exact expiry tick")
	}
	again, reused, err := planner.Alloc(2)
	if err != nil || again != 0 || reused != 1 {
		t.Fatalf("alloc at expiry = (%d,%d,%v), want reuse of tag 1", again, reused, err)
	}
}

// TestPartialIntervalReuse verifies that a tag is poisoned only for the
// address units where it was released. A disjoint interval that never hosted
// the tag stays reusable immediately, while a quarantined interval rejects
// explicitly instead of silently reusing the tag.
func TestPartialIntervalReuse(t *testing.T) {
	planner, _ := New(Config{TagDomainSize: 3, AddressSpace: 8, Quarantine: 5})

	// Build two live allocations separated by a permanently allocated guard so
	// their free ranges can never coalesce: [0,3) tag1, [3,4) tag2 guard,
	// [4,8) free and never touched by tag1.
	leftStart, leftTag, err := planner.Alloc(3)
	if err != nil || leftStart != 0 || leftTag != 1 {
		t.Fatalf("left Alloc = (%d,%d,%v)", leftStart, leftTag, err)
	}
	guardStart, guardTag, err := planner.Alloc(1)
	if err != nil || guardStart != 3 || guardTag != 2 {
		t.Fatalf("guard Alloc = (%d,%d,%v)", guardStart, guardTag, err)
	}

	// Release tag1 on [0,3): those units now poison tag1 until tick 5.
	if err := planner.Free(leftStart); err != nil {
		t.Fatalf("Free left: %v", err)
	}
	if planner.Eligible(1, 0, 3) {
		t.Fatal("tag 1 must remain poisoned on the released units")
	}
	// The disjoint tail [4,8) never hosted tag1 and must accept it now.
	if !planner.Eligible(1, 4, 8) {
		t.Fatal("tag 1 should be eligible on the never-poisoned disjoint interval")
	}

	// First fit is [0,3): tag1 is quarantined there, so the planner must skip
	// it and use the lowest fresh tag (tag2) rather than silently reuse tag1.
	gotStart, gotTag, err := planner.Alloc(3)
	if err != nil || gotStart != 0 || gotTag != 3 {
		t.Fatalf("skip-poisoned Alloc = (%d,%d,%v), want tag 3 at 0", gotStart, gotTag, err)
	}

	// The clean tail then reuses tag1 immediately: partial interval reuse.
	tailStart, tailTag, err := planner.Alloc(3)
	if err != nil || tailStart != 4 || tailTag != 1 {
		t.Fatalf("clean-tail Alloc = (%d,%d,%v), want tag 1 at 4", tailStart, tailTag, err)
	}

	// Release the tag3 allocation and ask for [0,3) again while its quarantine
	// is open. The planner must reject explicitly, never hand tag2 back.
	if err := planner.Free(0); err != nil {
		t.Fatalf("Free tag 3: %v", err)
	}
	planner.Clock().Advance(2) // tick 2; tag3 quarantine on [0,3) runs to 7
	blockedStart, blockedTag, err := planner.Alloc(3)
	if err == nil {
		t.Fatalf("quarantined alloc unexpectedly succeeded at %d with tag %d", blockedStart, blockedTag)
	}
	if !errors.Is(err, ErrRejected) {
		t.Fatalf("quarantined interval alloc = %v, want explicit rejection", err)
	}
	var exhausted *ExhaustedError
	if !errors.As(err, &exhausted) || !exhausted.Waitable || exhausted.WaitTick != 5 {
		t.Fatalf("rejection = %v, want waitable hint tick 5", err)
	}
}
