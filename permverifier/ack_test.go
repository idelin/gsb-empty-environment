package permverifier

import "testing"

func TestAckScopedByObjectAndGeneration(t *testing.T) {
	batch := []Op{
		{ID: 1, Alloc: &AllocOp{Phys: Interval{Lo: 0xD000, Len: 0x1000}}},
		{ID: 2, Map: &MapOp{
			View: 1, Object: 100, Gen: 1, Off: 0, Len: 0x1000,
			Perm: PermWrite, CPUs: []uint64{1},
			Segments: []Segment{{Off: 0, Len: 0x1000, PAddr: 0xD000}},
			Depends:  []uint64{1},
		}},
		{ID: 3, Revoke: &RevokeOp{Object: 100, Gen: 1, Depends: []uint64{2}}},
		// Wrong generation: must not release the gen-1 revoke.
		{ID: 4, Ack: &AckOp{CPU: 1, Object: 100, Gen: 2, Depends: []uint64{3}}},
	}
	rep, err := Verify(batch, opts())
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if rep.Verdict != VerdictIncomplete {
		t.Fatalf("mismatched ack must leave the revoke pending, got %s", rep.Verdict)
	}

	w := newWorld(opts().Pool)
	for _, op := range batch {
		w.apply(op)
	}
	if _, pending := w.revokes[genKey{100, 1}]; !pending {
		t.Fatal("gen-2 ack must not clear a gen-1 revoke")
	}
	w.applyAck(1, 100, 1)
	if len(w.views) != 0 {
		t.Fatal("correct ack must release the view")
	}
}

func TestWriteWithoutWritePermissionStuck(t *testing.T) {
	batch := []Op{
		{ID: 1, Alloc: &AllocOp{Phys: Interval{Lo: 0xE000, Len: 0x1000}}},
		{ID: 2, Map: &MapOp{
			View: 1, Object: 101, Gen: 1, Off: 0, Len: 0x1000,
			Perm: PermRead, CPUs: []uint64{1},
			Segments: []Segment{{Off: 0, Len: 0x1000, PAddr: 0xE000}},
			Depends:  []uint64{1},
		}},
		{ID: 3, Write: &WriteOp{
			View: 1, Object: 101, Gen: 1, Off: 0, Len: 8,
			Depends: []uint64{2},
		}},
	}
	rep, err := Verify(batch, opts())
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if rep.Verdict != VerdictIncomplete {
		t.Fatalf("write through a read-only view must be stuck, got %s", rep.Verdict)
	}
}

func TestWriteOutsideViewRangeStuck(t *testing.T) {
	batch := []Op{
		{ID: 1, Alloc: &AllocOp{Phys: Interval{Lo: 0xF000, Len: 0x1000}}},
		{ID: 2, Map: &MapOp{
			View: 1, Object: 102, Gen: 1, Off: 0, Len: 0x1000,
			Perm: PermWrite, CPUs: []uint64{1},
			Segments: []Segment{{Off: 0, Len: 0x1000, PAddr: 0xF000}},
			Depends:  []uint64{1},
		}},
		{ID: 3, Write: &WriteOp{
			View: 1, Object: 102, Gen: 1, Off: 0x800, Len: 0x2000,
			Depends: []uint64{2},
		}},
	}
	rep, err := Verify(batch, opts())
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if rep.Verdict != VerdictIncomplete {
		t.Fatalf("out-of-range write must never enable, got %s", rep.Verdict)
	}
}

func TestTopOfSpaceIntersection(t *testing.T) {
	last := uint64(0xFFFF_FFFF_FFFF_FFFF)
	a := Interval{Lo: last - 15, Len: 16}
	b := Interval{Lo: last - 3, Len: 4}
	got := a.Intersection(b)
	if got != b {
		t.Fatalf("tail intersection wrong: %+v", got)
	}
	c := Interval{Lo: 0, Len: 8}
	if a.Intersection(c).Len != 0 {
		t.Fatal("disjoint intervals must not intersect")
	}
}
