package handover

import "testing"

// 混合权限的三槽环：临时槽在携带能力跨越读/读写边界前必须遇到屏障。
func TestMixedPermCycleBarriers(t *testing.T) {
	tab := &Table{Handles: []Handle{
		{Slot: 1, Gen: 1, Object: "a", Perm: PermRead},
		{Slot: 2, Gen: 1, Object: "b", Perm: PermRead | PermWrite},
		{Slot: 3, Gen: 1, Object: "c", Perm: PermRead},
	}}
	roles := []Role{
		{Name: "ra", Target: 2, Allowed: Allow("a"), Need: PermRead, Share: ShareWith()},
		{Name: "rb", Target: 3, Allowed: Allow("b"), Need: PermRead | PermWrite, Share: ShareWith()},
		{Name: "rc", Target: 1, Allowed: Allow("c"), Need: PermRead, Share: ShareWith()},
	}
	p, err := BuildPlan(tab, roles, 99)
	if err != nil {
		t.Fatal(err)
	}

	tempLoaded := false
	crossBoundaries := 0
	tempEmptyAtEveryBarrier := true
	for _, op := range p.Ops {
		switch op.Kind {
		case OpLoad:
			tempLoaded = true
		case OpStore, OpDropTemp:
			tempLoaded = false
		case OpBarrier:
			if op.Bound == PermRead || op.Bound == (PermRead|PermWrite) {
				crossBoundaries++
			}
			if tempLoaded {
				tempEmptyAtEveryBarrier = false
			}
		}
	}
	if crossBoundaries == 0 {
		t.Fatalf("no read/rw permission boundary emitted:\n%s", dumpOps(p.Ops))
	}
	if !tempEmptyAtEveryBarrier {
		t.Fatalf("temp slot occupied at a permission boundary:\n%s", dumpOps(p.Ops))
	}
	if err := p.Validate(tab); err != nil {
		t.Fatalf("validate: %v", err)
	}

	mt := NewMemTable(tab)
	if err := p.Commit(mt); err != nil {
		t.Fatalf("commit: %v", err)
	}
	got := snapshotMap(mt)
	if got[2].Object != "a" || got[3].Object != "b" || got[1].Object != "c" {
		t.Fatalf("mixed perm cycle result wrong: %v", mt.Snapshot())
	}
}
