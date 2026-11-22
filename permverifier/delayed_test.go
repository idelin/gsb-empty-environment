package permverifier

import "testing"

// delayedAckBatch models a JIT view (W) replaced by an executable view of the
// same object on the same physical frame. The revoke is only released once the
// single CPU acknowledges the old generation.
func delayedAckBatch(includeAck bool) []Op {
	batch := []Op{
		{ID: 1, Alloc: &AllocOp{Phys: Interval{Lo: 0x4000, Len: 0x1000}}},
		{ID: 2, Map: &MapOp{
			View: 1, Object: 7, Gen: 1, Off: 0, Len: 0x1000,
			Perm: PermWrite, CPUs: []uint64{3},
			Segments: []Segment{{Off: 0, Len: 0x1000, PAddr: 0x4000}},
			Depends:  []uint64{1},
		}},
		{ID: 3, Revoke: &RevokeOp{Object: 7, Gen: 1, Depends: []uint64{2}}},
		{ID: 4, Map: &MapOp{
			View: 2, Object: 7, Gen: 2, Off: 0, Len: 0x1000,
			Perm: PermExec, CPUs: []uint64{3},
			Segments: []Segment{{Off: 0, Len: 0x1000, PAddr: 0x4000}},
			Depends:  []uint64{3},
		}},
	}
	if includeAck {
		batch = append(batch, Op{ID: 5, Ack: &AckOp{CPU: 3, Object: 7, Gen: 1, Depends: []uint64{3}}})
		batch[3].Map.Depends = []uint64{5}
	}
	return batch
}

func TestDelayedAckKeepsOldPermission(t *testing.T) {
	// With an ack, every interleaving is safe; without it, the executor can be
	// installed while the CPU still retains write permission.
	rep, err := Verify(delayedAckBatch(true), opts())
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if rep.Verdict != VerdictSafe {
		t.Fatalf("ack variant: want safe, got %s %s", rep.Verdict, rep.Reason)
	}

	rep, err = Verify(delayedAckBatch(false), opts())
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if rep.Verdict != VerdictUnsafe {
		t.Fatalf("no-ack variant: want unsafe, got %s %s", rep.Verdict, rep.Reason)
	}
	if got := rep.Conflict.Phys; got != (Interval{Lo: 0x4000, Len: 0x1000}) {
		t.Fatalf("unexpected conflict: %+v", got)
	}

	// Byte table mirror: retain W until the ack, then drop it and install X.
	pt := NewSmallPageTable()
	wRef := ViewRef{ID: 1, Gen: 1, CPU: 3, Perm: PermWrite}
	xRef := ViewRef{ID: 2, Gen: 2, CPU: 3, Perm: PermExec}
	frame := Interval{Lo: 0x4000, Len: 0x1000}
	pt.MapView(frame, wRef)
	if c := pt.Conflict(); c != nil {
		t.Fatalf("write-only frame must be clean: %+v", c)
	}
	// Revoke issued but ack lost: old W stays, new X appears -> conflict.
	pt.MapView(frame, xRef)
	if c := pt.Conflict(); c == nil || c.Phys != frame {
		t.Fatalf("retained W plus new X must conflict")
	}
	pt.UnmapView(xRef)
	pt.UnmapView(wRef)
	pt.MapView(frame, xRef)
	if c := pt.Conflict(); c != nil {
		t.Fatalf("after ack the frame may be executed: %+v", c.Phys)
	}
}

func TestPartialAckStillRetained(t *testing.T) {
	batch := []Op{
		{ID: 1, Alloc: &AllocOp{Phys: Interval{Lo: 0x5000, Len: 0x1000}}},
		{ID: 2, Map: &MapOp{
			View: 1, Object: 9, Gen: 1, Off: 0, Len: 0x1000,
			Perm: PermWrite, CPUs: []uint64{1, 2},
			Segments: []Segment{{Off: 0, Len: 0x1000, PAddr: 0x5000}},
			Depends:  []uint64{1},
		}},
		{ID: 3, Revoke: &RevokeOp{Object: 9, Gen: 1, Depends: []uint64{2}}},
		{ID: 4, Ack: &AckOp{CPU: 1, Object: 9, Gen: 1, Depends: []uint64{3}}},
		{ID: 5, Map: &MapOp{
			View: 2, Object: 9, Gen: 2, Off: 0, Len: 0x1000,
			Perm: PermExec, CPUs: []uint64{2},
			Segments: []Segment{{Off: 0, Len: 0x1000, PAddr: 0x5000}},
			Depends:  []uint64{3},
		}},
		{ID: 6, Ack: &AckOp{CPU: 2, Object: 9, Gen: 1, Depends: []uint64{3}}},
	}
	rep, err := Verify(batch, opts())
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	// CPU 2 never acks before gen-2 can be installed: it kept W while gaining X.
	if rep.Verdict != VerdictUnsafe {
		t.Fatalf("want unsafe interleaving, got %s %s", rep.Verdict, rep.Reason)
	}
}
