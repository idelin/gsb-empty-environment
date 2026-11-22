package permverifier

import "testing"

func TestFull64BitRanges(t *testing.T) {
	last := uint64(0xFFFF_FFFF_FFFF_FFFF)
	full := Interval{Lo: 1, Len: last}
	if _, ok := full.End(); ok {
		t.Fatalf("range past the final byte must overflow")
	}
	fullSpan := Interval{Lo: 0, Len: last}
	if end, ok := fullSpan.End(); !ok || end != last {
		t.Fatalf("[0,2^64-1) must be representable, got end=%d ok=%v", end, ok)
	}
	tail := Interval{Lo: last - 7, Len: 8}
	end, ok := tail.End()
	if ok || end != 0 {
		t.Fatalf("tail ending at 2^64 must report overflow sentinel: end=%d ok=%v", end, ok)
	}

	batch := []Op{
		{ID: 1, Alloc: &AllocOp{Phys: tail}},
		{ID: 2, Map: &MapOp{
			View: 1, Object: 70, Gen: 1, Off: 0, Len: 8,
			Perm: PermWrite, CPUs: []uint64{1},
			Segments: []Segment{{Off: 0, Len: 8, PAddr: last - 7}},
			Depends:  []uint64{1},
		}},
		{ID: 3, Map: &MapOp{
			View: 2, Object: 71, Gen: 1, Off: 0, Len: 8,
			Perm: PermExec, CPUs: []uint64{1},
			Segments: []Segment{{Off: 0, Len: 8, PAddr: last - 7}},
			Depends:  []uint64{1},
		}},
	}
	rep, err := Verify(batch, opts())
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if rep.Verdict != VerdictUnsafe || rep.Conflict.Phys != tail {
		t.Fatalf("top-of-space tail conflict wrong: %+v err=%v", rep, err)
	}

	// Overflowing construction is rejected at validation rather than wrapped.
	bad := []Op{{
		ID: 1, Alloc: &AllocOp{Phys: Interval{Lo: 2, Len: last}},
	}}
	if _, err := Verify(bad, opts()); err == nil {
		t.Fatal("overflowing alloc must be rejected")
	}
}

func TestBudgetExhaustionUnknown(t *testing.T) {
	// Independent ops with no edges produce every permutation of prefixes.
	var batch []Op
	for i := uint64(1); i <= 8; i++ {
		batch = append(batch,
			Op{ID: i * 10, Alloc: &AllocOp{Phys: Interval{Lo: i << 20, Len: 0x1000}}},
			Op{ID: i*10 + 1, Map: &MapOp{
				View: i, Object: 80 + i, Gen: 1, Off: 0, Len: 0x1000,
				Perm: PermRead, CPUs: []uint64{1},
				Segments: []Segment{{Off: 0, Len: 0x1000, PAddr: i << 20}},
				Depends:  []uint64{i * 10},
			}},
		)
	}
	tight := Options{MaxStates: 5, MaxSteps: 5, Pool: opts().Pool}
	rep, err := Verify(batch, tight)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if rep.Verdict != VerdictUnknown {
		t.Fatalf("want unknown under tight budget, got %s", rep.Verdict)
	}
	if rep.StatesExplored == 0 {
		t.Fatal("unknown report must record explored states")
	}
}

func TestIncompleteDanglingRevokeAndDeadOp(t *testing.T) {
	batch := []Op{
		{ID: 1, Alloc: &AllocOp{Phys: Interval{Lo: 0xB000, Len: 0x1000}}},
		{ID: 2, Map: &MapOp{
			View: 1, Object: 90, Gen: 1, Off: 0, Len: 0x1000,
			Perm: PermWrite, CPUs: []uint64{1},
			Segments: []Segment{{Off: 0, Len: 0x1000, PAddr: 0xB000}},
			Depends:  []uint64{1},
		}},
		{ID: 3, Revoke: &RevokeOp{Object: 90, Gen: 1, Depends: []uint64{2}}},
	}
	rep, err := Verify(batch, opts())
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if rep.Verdict != VerdictIncomplete {
		t.Fatalf("missing ack must be incomplete, got %s", rep.Verdict)
	}

	// A free whose exact span never exists cannot be enabled in any interleaving.
	dead := []Op{
		{ID: 1, Free: &FreeOp{Phys: Interval{Lo: 0xC000, Len: 0x1000}}},
	}
	rep, err = Verify(dead, opts())
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if rep.Verdict != VerdictIncomplete {
		t.Fatalf("dead op must be incomplete, got %s", rep.Verdict)
	}
}

func TestMalformedBatchesRejected(t *testing.T) {
	cases := map[string][]Op{
		"duplicate id": {
			{ID: 1, Alloc: &AllocOp{Phys: Interval{Lo: 1, Len: 1}}},
			{ID: 1, Alloc: &AllocOp{Phys: Interval{Lo: 4, Len: 1}}},
		},
		"unknown dependency": {
			{ID: 1, Alloc: &AllocOp{Phys: Interval{Lo: 1, Len: 1}, Depends: []uint64{9}}},
		},
		"segment hole": {
			{ID: 1, Alloc: &AllocOp{Phys: Interval{Lo: 0, Len: 0x20}}},
			{ID: 2, Map: &MapOp{
				View: 1, Object: 1, Gen: 1, Off: 0, Len: 0x20,
				Perm: PermWrite, CPUs: []uint64{1},
				Segments: []Segment{
					{Off: 0, Len: 0x10, PAddr: 0},
					{Off: 0x18, Len: 0x8, PAddr: 0x10},
				},
				Depends: []uint64{1},
			}},
		},
		"cow and segments": {
			{ID: 1, Alloc: &AllocOp{Phys: Interval{Lo: 0, Len: 0x1000}}},
			{ID: 2, Map: &MapOp{
				View: 1, Object: 1, Gen: 1, Off: 0, Len: 0x10,
				Perm: PermWrite, CPUs: []uint64{1},
				Segments: []Segment{{Off: 0, Len: 0x10, PAddr: 0}},
				Cow:      &CowSpec{PAddr: 0, Len: 0x10, Page: 0x10},
				Depends:  []uint64{1},
			}},
		},
		"cycle": {
			{ID: 1, Alloc: &AllocOp{Phys: Interval{Lo: 1, Len: 1}, Depends: []uint64{2}}},
			{ID: 2, Alloc: &AllocOp{Phys: Interval{Lo: 4, Len: 1}, Depends: []uint64{1}}},
		},
	}
	for name, batch := range cases {
		if _, err := Verify(batch, opts()); err == nil {
			t.Fatalf("%s: expected validation error", name)
		}
	}
}

func TestPoolTooSmallRejected(t *testing.T) {
	batch := []Op{
		{ID: 1, Alloc: &AllocOp{Phys: Interval{Lo: 0x30000, Len: 2 * cowPage}}},
		{ID: 2, Map: &MapOp{
			View: 1, Object: 95, Gen: 1, Off: 0, Len: 2 * cowPage,
			Perm: PermWrite, CPUs: []uint64{1},
			Cow:     &CowSpec{PAddr: 0x30000, Len: 2 * cowPage, Page: cowPage},
			Depends: []uint64{1},
		}},
		{ID: 3, Write: &WriteOp{
			View: 1, Object: 95, Gen: 1, Off: 0, Len: 2 * cowPage,
			Depends: []uint64{2},
		}},
	}
	tight := Options{MaxStates: 100, MaxSteps: 20, Pool: Interval{Lo: 0x8000_0000, Len: cowPage}}
	if _, err := Verify(batch, tight); err == nil {
		t.Fatal("pool smaller than split demand must be rejected")
	}
}

func TestSmallPageTableLifecycle(t *testing.T) {
	pt := NewSmallPageTable()
	a := ViewRef{ID: 1, Gen: 1, CPU: 1, Perm: PermWrite}
	b := ViewRef{ID: 2, Gen: 1, CPU: 1, Perm: PermExec}
	pt.MapView(Interval{Lo: 100, Len: 10}, a)
	pt.MapView(Interval{Lo: 105, Len: 10}, b)
	c := pt.Conflict()
	if c == nil || c.Phys != (Interval{Lo: 105, Len: 5}) {
		t.Fatalf("overlap must be 105..110, got %+v", c)
	}
	pt.UnmapView(b)
	if c := pt.Conflict(); c != nil {
		t.Fatalf("after unmap no conflict may remain")
	}
	if pt.Size() != 10 {
		t.Fatalf("write view must still occupy 10 bytes, got %d", pt.Size())
	}
	pt.UnmapView(a)
	if pt.Size() != 0 {
		t.Fatalf("fully unmapped table must be empty")
	}
}
