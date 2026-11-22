package permverifier

import "testing"

func TestShortestFailingPrefixAcrossInterleavings(t *testing.T) {
	// Two independent alias pairs. The second pair alone conflicts after two
	// maps; the first pair needs three ops before its conflict appears. BFS
	// must report the shorter failing sequence regardless of batch ordering.
	batch := []Op{
		{ID: 1, Alloc: &AllocOp{Phys: Interval{Lo: 0x11000, Len: 0x1000}}},
		{ID: 2, Alloc: &AllocOp{Phys: Interval{Lo: 0x12000, Len: 0x1000}}},
		{ID: 3, Map: &MapOp{
			View: 1, Object: 200, Gen: 1, Off: 0, Len: 0x1000,
			Perm: PermWrite, CPUs: []uint64{1},
			Segments: []Segment{{Off: 0, Len: 0x1000, PAddr: 0x11000}},
			Depends:  []uint64{1},
		}},
		{ID: 4, Map: &MapOp{
			View: 2, Object: 201, Gen: 1, Off: 0, Len: 0x1000,
			Perm: PermExec, CPUs: []uint64{1},
			Segments: []Segment{{Off: 0, Len: 0x1000, PAddr: 0x11000}},
			Depends:  []uint64{1},
		}},
		{ID: 5, Map: &MapOp{
			View: 3, Object: 202, Gen: 1, Off: 0, Len: 0x1000,
			Perm: PermWrite, CPUs: []uint64{2},
			Segments: []Segment{{Off: 0, Len: 0x1000, PAddr: 0x12000}},
			Depends:  []uint64{2},
		}},
		{ID: 6, Map: &MapOp{
			View: 4, Object: 203, Gen: 1, Off: 0, Len: 0x1000,
			Perm: PermExec, CPUs: []uint64{2},
			Segments: []Segment{{Off: 0, Len: 0x1000, PAddr: 0x12000}},
			Depends:  []uint64{2},
		}},
	}
	rep, err := Verify(batch, opts())
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if rep.Verdict != VerdictUnsafe {
		t.Fatalf("want unsafe, got %s", rep.Verdict)
	}
	// Shortest path: one alloc then its two aliasing maps = three ops.
	if len(rep.Prefix) != 3 {
		t.Fatalf("shortest prefix must have 3 ops, got %v", rep.Prefix)
	}
	// Both conflicts are depth three; deterministically take the batch-first
	// frame so reports are reproducible.
	if rep.Conflict.Phys.Lo != 0x11000 {
		t.Fatalf("first conflict should be the lowest frame, got %+v", rep.Conflict.Phys)
	}
}

func TestSafeInterleavingWithIndependentOps(t *testing.T) {
	batch := []Op{
		{ID: 1, Alloc: &AllocOp{Phys: Interval{Lo: 0x13000, Len: 0x2000}}},
		{ID: 2, Map: &MapOp{
			View: 1, Object: 210, Gen: 1, Off: 0, Len: 0x1000,
			Perm: PermWrite, CPUs: []uint64{1},
			Segments: []Segment{{Off: 0, Len: 0x1000, PAddr: 0x13000}},
			Depends:  []uint64{1},
		}},
		{ID: 3, Map: &MapOp{
			View: 2, Object: 211, Gen: 1, Off: 0, Len: 0x1000,
			Perm: PermExec, CPUs: []uint64{1},
			Segments: []Segment{{Off: 0, Len: 0x1000, PAddr: 0x14000}},
			Depends:  []uint64{1},
		}},
	}
	rep, err := Verify(batch, opts())
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if rep.Verdict != VerdictSafe || rep.StatesExplored < 3 {
		t.Fatalf("all interleavings of disjoint frames are safe: %+v", rep)
	}
}
