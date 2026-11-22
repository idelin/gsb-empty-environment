package permverifier

import "testing"

func opts() Options {
	return Options{
		MaxStates: 10000,
		MaxSteps:  100,
		Pool:      Interval{Lo: 0x8000_0000_0000, Len: 1 << 30},
	}
}

func TestAliasImmediateConflict(t *testing.T) {
	batch := []Op{
		{ID: 1, Alloc: &AllocOp{Phys: Interval{Lo: 0x1000, Len: 0x1000}}},
		{ID: 2, Map: &MapOp{
			View: 1, Object: 10, Gen: 1, Off: 0, Len: 0x1000,
			Perm: PermWrite, CPUs: []uint64{1},
			Segments: []Segment{{Off: 0, Len: 0x1000, PAddr: 0x1000}},
			Depends:  []uint64{1},
		}},
		{ID: 3, Map: &MapOp{
			View: 2, Object: 11, Gen: 1, Off: 0, Len: 0x1000,
			Perm: PermExec, CPUs: []uint64{1},
			Segments: []Segment{{Off: 0, Len: 0x1000, PAddr: 0x1000}},
			Depends:  []uint64{1},
		}},
	}
	rep, err := Verify(batch, opts())
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if rep.Verdict != VerdictUnsafe {
		t.Fatalf("want unsafe, got %s (%s)", rep.Verdict, rep.Reason)
	}
	if rep.Conflict.Phys != (Interval{Lo: 0x1000, Len: 0x1000}) {
		t.Fatalf("bad conflict interval: %+v", rep.Conflict.Phys)
	}
	if len(rep.Prefix) != 3 || rep.Prefix[2] != 3 {
		t.Fatalf("shortest prefix wrong: %v", rep.Prefix)
	}

	pt := NewSmallPageTable()
	pt.MapView(Interval{Lo: 0x1000, Len: 0x1000}, ViewRef{ID: 1, Gen: 1, CPU: 1, Perm: PermWrite})
	pt.MapView(Interval{Lo: 0x1000, Len: 0x1000}, ViewRef{ID: 2, Gen: 1, CPU: 1, Perm: PermExec})
	got := pt.Conflict()
	if got == nil || got.Phys != rep.Conflict.Phys {
		t.Fatalf("small table mismatch: %+v", got)
	}
}

func TestDistinctFramesAreSafe(t *testing.T) {
	batch := []Op{
		{ID: 1, Alloc: &AllocOp{Phys: Interval{Lo: 0x1000, Len: 0x2000}}},
		{ID: 2, Map: &MapOp{
			View: 1, Object: 10, Gen: 1, Off: 0, Len: 0x1000,
			Perm: PermWrite, CPUs: []uint64{1},
			Segments: []Segment{{Off: 0, Len: 0x1000, PAddr: 0x1000}},
			Depends:  []uint64{1},
		}},
		{ID: 3, Map: &MapOp{
			View: 2, Object: 11, Gen: 1, Off: 0, Len: 0x1000,
			Perm: PermExec, CPUs: []uint64{1},
			Segments: []Segment{{Off: 0, Len: 0x1000, PAddr: 0x2000}},
			Depends:  []uint64{1},
		}},
	}
	rep, err := Verify(batch, opts())
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if rep.Verdict != VerdictSafe {
		t.Fatalf("want safe, got %s: %s", rep.Verdict, rep.Reason)
	}
}
