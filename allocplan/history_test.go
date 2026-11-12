package allocplan

import "testing"

// TestSplitRetainsHistory requires each piece produced by Split to keep the
// per-unit quarantine lineage recorded before the split.
func TestSplitRetainsHistory(t *testing.T) {
	planner, _ := New(Config{TagDomainSize: 2, AddressSpace: 6, Quarantine: 4})
	start, tag, err := planner.Alloc(4)
	if err != nil || tag != 1 {
		t.Fatalf("Alloc = (%d,%d,%v)", start, tag, err)
	}
	if err := planner.Free(0); err != nil {
		t.Fatalf("Free: %v", err)
	}
	// Reallocate the range with tag 2 while the tag 1 quarantine stays in the
	// per-unit history.
	restart, retag, err := planner.Alloc(4)
	if err != nil || restart != 0 || retag != 2 {
		t.Fatalf("re-Alloc = (%d,%d,%v)", restart, retag, err)
	}
	mid, err := planner.Split(0, 2)
	if err != nil || mid != 2 {
		t.Fatalf("Split = (%d,%v), want mid 2", mid, err)
	}
	for _, addr := range []int{0, 1, 2, 3} {
		until, ok := planner.QuarantineUntil(addr, 1)
		if !ok || until != 4 {
			t.Fatalf("address %d lost history: expiry=%d ok=%v, want 4", addr, until, ok)
		}
	}
	segments := planner.Segments()
	if len(segments) != 3 || segments[0].Tag != 2 || segments[1].Tag != 2 || segments[2].Tag != NoTag {
		t.Fatalf("segments after split = %+v, want two tag-2 pieces plus free tail", segments)
	}
}

// TestMergeRetainsHistory makes adjacent free segments coalesce without
// dropping or widening per-unit quarantine, and proves repeated re-releases
// extend rather than shorten the recorded expiry.
func TestMergeRetainsHistory(t *testing.T) {
	planner, _ := New(Config{TagDomainSize: 3, AddressSpace: 6, Quarantine: 4})

	first, tagA, err := planner.Alloc(2)
	if err != nil || tagA != 1 {
		t.Fatalf("Alloc A = (%d,%d,%v)", first, tagA, err)
	}
	second, tagB, err := planner.Alloc(2)
	if err != nil || second != 2 || tagB != 2 {
		t.Fatalf("Alloc B = (%d,%d,%v)", second, tagB, err)
	}
	// Keep tag 2 live as a guard; release tag 1 so [0,2) coalesces only with
	// the untouched free tail [4,6) after the guard is skipped.
	if err := planner.Free(0); err != nil {
		t.Fatalf("Free A: %v", err)
	}

	segments := planner.Segments()
	if len(segments) != 3 {
		t.Fatalf("segments = %d, want free [0,2), guard [2,4), free tail [4,6)", len(segments))
	}
	if segments[0].Tag != NoTag || segments[1].Tag != 2 || segments[2].Tag != NoTag {
		t.Fatalf("segment layout = %+v", segments)
	}
	// Tag 1 is poisoned only on its former units, never on the clean tail.
	for addr := 0; addr < 2; addr++ {
		if until, _ := planner.QuarantineUntil(addr, 1); until != 4 {
			t.Fatalf("tag 1 at addr %d expiry = %d, want 4", addr, until)
		}
	}
	if _, poisoned := planner.QuarantineUntil(4, 1); poisoned {
		t.Fatal("tag 1 wrongly widened onto the clean tail after coalescing")
	}

	// Reallocate the freed [0,2): tag 1 is quarantined there and tag 2 is live
	// in the guard, so the lowest fresh tag is 3.
	restart, retag, err := planner.Alloc(2)
	if err != nil || restart != 0 || retag != 3 {
		t.Fatalf("Alloc C = (%d,%d,%v), want tag 3 at 0", restart, retag, err)
	}
	planner.Clock().Advance(3) // tick 3
	if err := planner.Free(0); err != nil {
		t.Fatalf("Free C: %v", err)
	}
	until, ok := planner.QuarantineUntil(0, 3)
	if !ok || until != 7 {
		t.Fatalf("tag 3 expiry = %d (ok=%v), want 7", until, ok)
	}
	// The earlier tag 1 history on the same units is retained alongside the
	// newer tag 3 entry.
	tag1Until, ok := planner.QuarantineUntil(0, 1)
	if !ok || tag1Until != 4 {
		t.Fatalf("tag 1 retained history = %d (ok=%v), want 4", tag1Until, ok)
	}
}
