package permverifier

import "testing"

func rebuildBatch(ackBeforeRemap bool) []Op {
	batch := []Op{
		{ID: 1, Alloc: &AllocOp{Phys: Interval{Lo: 0x7000, Len: 0x1000}}},
		{ID: 2, Map: &MapOp{
			View: 1, Object: 40, Gen: 1, Off: 0, Len: 0x1000,
			Perm: PermWrite, CPUs: []uint64{5},
			Segments: []Segment{{Off: 0, Len: 0x1000, PAddr: 0x7000}},
			Depends:  []uint64{1},
		}},
		{ID: 3, Revoke: &RevokeOp{Object: 40, Gen: 1, Depends: []uint64{2}}},
		{ID: 4, Ack: &AckOp{CPU: 5, Object: 40, Gen: 1, Depends: []uint64{3}}},
	}
	remap := Op{ID: 5, Remap: &RemapOp{
		Map: MapOp{
			View: 2, Object: 40, Gen: 2, Off: 0, Len: 0x1000,
			Perm: PermExec, CPUs: []uint64{5},
			Segments: []Segment{{Off: 0, Len: 0x1000, PAddr: 0x7000}},
			Depends:  []uint64{3},
		},
	}}
	if ackBeforeRemap {
		remap.Remap.Map.Depends = []uint64{4}
		batch[3].Ack.Depends = []uint64{3}
	} else {
		batch = batch[:3]
	}
	batch = append(batch, remap)
	return batch
}

func TestAliasRebuildAfterRevoke(t *testing.T) {
	rep, err := Verify(rebuildBatch(true), opts())
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if rep.Verdict != VerdictSafe {
		t.Fatalf("fully acked rebuild must be safe, got %s %s", rep.Verdict, rep.Reason)
	}

	rep, err = Verify(rebuildBatch(false), opts())
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if rep.Verdict != VerdictUnsafe {
		t.Fatalf("rebuild before ack must be unsafe, got %s %s", rep.Verdict, rep.Reason)
	}
	if rep.Conflict.Phys != (Interval{Lo: 0x7000, Len: 0x1000}) {
		t.Fatalf("conflict interval wrong: %+v", rep.Conflict.Phys)
	}

	// Byte table mirror of both orderings.
	frame := Interval{Lo: 0x7000, Len: 0x1000}
	old := ViewRef{ID: 1, Gen: 1, CPU: 5, Perm: PermWrite}
	next := ViewRef{ID: 2, Gen: 2, CPU: 5, Perm: PermExec}
	pt := NewSmallPageTable()
	pt.MapView(frame, old)
	pt.UnmapView(old) // ack arrived
	pt.MapView(frame, next)
	if c := pt.Conflict(); c != nil {
		t.Fatalf("acked rebuild clean: %+v", c.Phys)
	}
	pt2 := NewSmallPageTable()
	pt2.MapView(frame, old)
	pt2.MapView(frame, next) // ack lost
	if c := pt2.Conflict(); c == nil || c.Phys != frame {
		t.Fatalf("lost ack must leave a conflict")
	}
}

func TestPhysicalAddressReuse(t *testing.T) {
	batch := []Op{
		{ID: 1, Alloc: &AllocOp{Phys: Interval{Lo: 0x9000, Len: 0x1000}}},
		{ID: 2, Map: &MapOp{
			View: 1, Object: 50, Gen: 1, Off: 0, Len: 0x1000,
			Perm: PermExec, CPUs: []uint64{1},
			Segments: []Segment{{Off: 0, Len: 0x1000, PAddr: 0x9000}},
			Depends:  []uint64{1},
		}},
		// Optimistic allocator reuses the frame while the executor view is live.
		{ID: 3, Alloc: &AllocOp{
			Phys: Interval{Lo: 0x9000, Len: 0x1000}, Reuse: true,
			Depends: []uint64{2},
		}},
		{ID: 4, Map: &MapOp{
			View: 2, Object: 51, Gen: 1, Off: 0, Len: 0x1000,
			Perm: PermWrite, CPUs: []uint64{1},
			Segments: []Segment{{Off: 0, Len: 0x1000, PAddr: 0x9000}},
			Depends:  []uint64{3},
		}},
	}
	rep, err := Verify(batch, opts())
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if rep.Verdict != VerdictUnsafe {
		t.Fatalf("reuse over a live exec view must be unsafe, got %s %s", rep.Verdict, rep.Reason)
	}
	if rep.Conflict.Phys != (Interval{Lo: 0x9000, Len: 0x1000}) {
		t.Fatalf("reuse conflict interval wrong: %+v", rep.Conflict.Phys)
	}

	pt := NewSmallPageTable()
	pt.MapView(Interval{Lo: 0x9000, Len: 0x1000}, ViewRef{ID: 1, Gen: 1, CPU: 1, Perm: PermExec})
	pt.MapView(Interval{Lo: 0x9000, Len: 0x1000}, ViewRef{ID: 2, Gen: 1, CPU: 1, Perm: PermWrite})
	if c := pt.Conflict(); c == nil || c.Phys.Lo != 0x9000 {
		t.Fatalf("byte table must flag the reused frame")
	}
}

func TestSegmentedRemap(t *testing.T) {
	batch := []Op{
		{ID: 1, Alloc: &AllocOp{Phys: Interval{Lo: 0xA000, Len: 0x3000}}},
		{ID: 2, Map: &MapOp{
			View: 1, Object: 60, Gen: 1, Off: 0, Len: 0x2000,
			Perm: PermWrite, CPUs: []uint64{1},
			Segments: []Segment{
				{Off: 0, Len: 0x1000, PAddr: 0xA000},
				{Off: 0x1000, Len: 0x1000, PAddr: 0xB000},
			},
			Depends: []uint64{1},
		}},
		// Second generation keeps the first segment but aliases it as X.
		{ID: 3, Map: &MapOp{
			View: 2, Object: 60, Gen: 2, Off: 0, Len: 0x2000,
			Perm: PermExec, CPUs: []uint64{1},
			Segments: []Segment{
				{Off: 0, Len: 0x1000, PAddr: 0xA000},
				{Off: 0x1000, Len: 0x1000, PAddr: 0xC000},
			},
			Depends: []uint64{1},
		}},
	}
	rep, err := Verify(batch, opts())
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if rep.Verdict != VerdictUnsafe {
		t.Fatalf("shared segment must conflict, got %s", rep.Verdict)
	}
	if rep.Conflict.Phys != (Interval{Lo: 0xA000, Len: 0x1000}) {
		t.Fatalf("first shared segment expected, got %+v", rep.Conflict.Phys)
	}
}
