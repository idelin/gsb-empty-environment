package handover

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

func assertPlan(t *testing.T, tab *Table, roles []Role, temp int, wantSlots []int, wantObjects int) *Plan {
	t.Helper()
	p, err := BuildPlan(tab, roles, temp)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if got := p.UsedSlots; !equalInts(got, wantSlots) {
		t.Fatalf("slots = %v, want %v", got, wantSlots)
	}
	if len(p.UsedObjects) != wantObjects {
		t.Fatalf("object count = %d, want %d (%v)", len(p.UsedObjects), wantObjects, p.UsedObjects)
	}
	if err := p.Validate(tab); err != nil {
		t.Fatalf("Validate: %v\nops: %s", err, dumpOps(p.Ops))
	}
	return p
}

// 场景一：局部最小匹配会导致整体无解，规划器必须回溯到 2 对象解。
func TestLocalMinimumGreedyTrap(t *testing.T) {
	tab := &Table{Handles: []Handle{
		{Slot: 1, Gen: 1, Object: "x", Perm: PermRead},
		{Slot: 2, Gen: 1, Object: "x", Perm: PermRead},
		{Slot: 3, Gen: 1, Object: "y", Perm: PermRead},
	}}
	roles := []Role{
		{Name: "r1", Target: 10, Allowed: Allow("x", "y"), Need: PermRead, Share: ShareWith()},
		{Name: "r2", Target: 11, Allowed: Allow("x"), Need: PermRead, Share: ShareWith()},
	}
	// 贪心：r1 取槽1（对象 x），r2 只能取槽2，但共享被禁止 -> 无解。
	// 最优：r1 取槽3（对象 y），r2 取槽1（对象 x）。
	assertPlan(t, tab, roles, 99, []int{3, 1}, 2)
}

// 场景二：同对象上的可写别名不得交接给只需要读权限的角色。
func TestExtraWriteAliasRejected(t *testing.T) {
	tab := &Table{Handles: []Handle{
		{Slot: 1, Gen: 1, Object: "a", Perm: PermRead | PermWrite, Cloexec: false},
		{Slot: 2, Gen: 1, Object: "a", Perm: PermRead, Cloexec: true},
		{Slot: 3, Gen: 1, Object: "b", Perm: PermRead | PermWrite},
	}}
	roles := []Role{
		{Name: "reader", Target: 20, Allowed: Allow("a"), Need: PermRead, Share: ShareWith()},
		{Name: "writer", Target: 21, Allowed: Allow("b"), Need: PermRead | PermWrite, Share: ShareWith()},
	}
	p := assertPlan(t, tab, roles, 99, []int{2, 3}, 2)

	if p.Assignments[0].Slot != 2 {
		t.Fatalf("reader picked writable alias slot %d", p.Assignments[0].Slot)
	}

	onlyWritable := &Table{Handles: []Handle{tab.Handles[0]}}
	if _, err := BuildPlan(onlyWritable, roles[:1], 99); !errors.Is(err, ErrNoAssignment) {
		t.Fatalf("want ErrNoAssignment for extra-perm handle, got %v", err)
	}
}

// 场景三：三个占用槽构成目标环，必须借助唯一临时槽旋转。
func TestThreeSlotCycle(t *testing.T) {
	tab := &Table{Handles: []Handle{
		{Slot: 1, Gen: 1, Object: "a", Perm: PermRead},
		{Slot: 2, Gen: 2, Object: "b", Perm: PermRead},
		{Slot: 3, Gen: 3, Object: "c", Perm: PermRead},
	}}
	roles := []Role{
		{Name: "ra", Target: 2, Allowed: Allow("a"), Need: PermRead, Share: ShareWith()},
		{Name: "rb", Target: 3, Allowed: Allow("b"), Need: PermRead, Share: ShareWith()},
		{Name: "rc", Target: 1, Allowed: Allow("c"), Need: PermRead, Share: ShareWith()},
	}
	p := assertPlan(t, tab, roles, 99, []int{1, 2, 3}, 3)

	sawLoad, sawStore, drops := false, false, 0
	for _, op := range p.Ops {
		switch op.Kind {
		case OpLoad:
			if op.A != 99 {
				t.Fatalf("load used slot %d, not temp 99", op.A)
			}
			sawLoad = true
		case OpStore:
			sawStore = true
		case OpDropTemp:
			drops++
		}
	}
	if !sawLoad || !sawStore || drops != 1 {
		t.Fatalf("cycle must use exactly one temp rotation, ops:\n%s", dumpOps(p.Ops))
	}

	mt := NewMemTable(tab)
	if err := p.Commit(mt); err != nil {
		t.Fatalf("commit: %v", err)
	}
	got := snapshotMap(mt)
	if got[2].Object != "a" || got[3].Object != "b" || got[1].Object != "c" {
		t.Fatalf("cycle result wrong: %v", mt.Snapshot())
	}
}

// 场景四：互不相交的二元环合并为一次并发原子交换。
func TestConcurrentSwap(t *testing.T) {
	tab := &Table{Handles: []Handle{
		{Slot: 1, Gen: 1, Object: "a", Perm: PermRead},
		{Slot: 2, Gen: 1, Object: "b", Perm: PermRead},
		{Slot: 3, Gen: 1, Object: "c", Perm: PermRead},
		{Slot: 4, Gen: 1, Object: "d", Perm: PermRead},
	}}
	roles := []Role{
		{Name: "ra", Target: 2, Allowed: Allow("a"), Need: PermRead, Share: ShareWith()},
		{Name: "rb", Target: 1, Allowed: Allow("b"), Need: PermRead, Share: ShareWith()},
		{Name: "rc", Target: 4, Allowed: Allow("c"), Need: PermRead, Share: ShareWith()},
		{Name: "rd", Target: 3, Allowed: Allow("d"), Need: PermRead, Share: ShareWith()},
	}
	p := assertPlan(t, tab, roles, 99, []int{1, 2, 3, 4}, 4)

	batches := 0
	for _, op := range p.Ops {
		if op.Kind == OpParSwap {
			batches++
			if len(op.Pairs) != 2 {
				t.Fatalf("want one batch of 2 disjoint pairs, got %d", len(op.Pairs))
			}
		}
	}
	if batches != 1 {
		t.Fatalf("want exactly one parswap batch, ops:\n%s", dumpOps(p.Ops))
	}

	mt := NewMemTable(tab)
	if err := mt.ParSwap([][2]int{{1, 2}, {2, 3}}); err == nil {
		t.Fatal("overlapping parallel swap must fail")
	}
}

// 场景五：安装中途失败必须整体撤销；代次/身份不一致必须拒绝交接。
func TestMidInstallFailureAndTamper(t *testing.T) {
	tab := &Table{Handles: []Handle{
		{Slot: 1, Gen: 1, Object: "a", Perm: PermRead},
		{Slot: 2, Gen: 1, Object: "b", Perm: PermRead},
	}}
	roles := []Role{
		{Name: "r1", Target: 10, Allowed: Allow("a"), Need: PermRead, Share: ShareWith()},
		{Name: "r2", Target: 11, Allowed: Allow("b"), Need: PermRead, Share: ShareWith()},
	}
	p, err := BuildPlan(tab, roles, 99)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	before := NewMemTable(tab)
	beforeSnap := before.Snapshot()
	if err := p.CommitWithFault(before, 1); !errors.Is(err, ErrCommitFailed) {
		t.Fatalf("want ErrCommitFailed, got %v", err)
	}
	if !equalHandles(before.Snapshot(), beforeSnap) {
		t.Fatalf("rollback did not restore table:\n%v\n%v", before.Snapshot(), beforeSnap)
	}

	tamperedTab := &Table{Handles: []Handle{
		{Slot: 1, Gen: 2, Object: "a", Perm: PermRead},
		{Slot: 2, Gen: 1, Object: "z", Perm: PermRead},
	}}
	tampered := NewMemTable(tamperedTab)
	if err := p.Commit(tampered); !errors.Is(err, ErrVerifyFailed) {
		t.Fatalf("want ErrVerifyFailed, got %v", err)
	}
	if !equalHandles(tampered.Snapshot(), NewMemTable(tamperedTab).Snapshot()) {
		t.Fatal("failed verification must leave table untouched")
	}
}

// 权限边界：临时槽在跨权限前必须为空，未选句柄最终全部 cloexec。
func TestBarrierAndCloexecInvariant(t *testing.T) {
	tab := &Table{Handles: []Handle{
		{Slot: 1, Gen: 1, Object: "a", Perm: PermRead},
		{Slot: 2, Gen: 1, Object: "b", Perm: PermRead | PermWrite},
		{Slot: 3, Gen: 1, Object: "junk", Perm: PermRead},
	}}
	roles := []Role{
		{Name: "reader", Target: 2, Allowed: Allow("a"), Need: PermRead, Share: ShareWith()},
		{Name: "writer", Target: 1, Allowed: Allow("b"), Need: PermRead | PermWrite, Share: ShareWith()},
	}
	p, err := BuildPlan(tab, roles, 99)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if err := p.Validate(tab); err != nil {
		t.Fatalf("validate: %v", err)
	}

	bad := *p
	bad.Ops = append([]Op{{Kind: OpLoad, A: 99, B: 1}}, p.Ops...)
	if err := bad.Validate(tab); err == nil {
		t.Fatal("validator must reject temp slot left occupied across plan steps")
	}

	mt := NewMemTable(tab)
	if err := p.Commit(mt); err != nil {
		t.Fatalf("commit: %v", err)
	}
	for _, h := range mt.Snapshot() {
		if (h.Object == "a" || h.Object == "b") && h.Cloexec {
			t.Fatalf("selected %s must be inherited", h.Object)
		}
		if h.Object == "junk" && !h.Cloexec {
			t.Fatal("unselected handle must be cloexec")
		}
	}
}

// 随机小表交叉核验：规划器结果必须与独立穷举完全一致，且计划始终可模拟。
func TestFuzzAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(20261121))
	objects := []ObjectID{"o1", "o2", "o3", "o4"}
	perms := []Perm{PermRead, PermWrite, PermRead | PermWrite}

	for iter := 0; iter < 300; iter++ {
		n := 1 + rng.Intn(5)
		tab := &Table{}
		usedSlots := map[int]struct{}{}
		for i := 0; i < n; i++ {
			slot := 1 + rng.Intn(8)
			if _, dup := usedSlots[slot]; dup {
				continue
			}
			usedSlots[slot] = struct{}{}
			tab.Handles = append(tab.Handles, Handle{
				Slot:   slot,
				Gen:    uint64(1 + rng.Intn(5)),
				Object: objects[rng.Intn(len(objects))],
				Perm:   perms[rng.Intn(len(perms))],
			})
		}
		if len(tab.Handles) == 0 {
			continue
		}

		rn := 1 + rng.Intn(len(tab.Handles))
		roles := make([]Role, rn)
		targets := rng.Perm(30)
		for i := range roles {
			allowedCount := 1 + rng.Intn(len(objects))
			shuffled := append([]ObjectID(nil), objects...)
			rng.Shuffle(len(shuffled), func(a, b int) { shuffled[a], shuffled[b] = shuffled[b], shuffled[a] })
			roles[i] = Role{
				Name:    fmt.Sprintf("role%d", i),
				Target:  50 + targets[i],
				Allowed: Allow(shuffled[:allowedCount]...),
				Need:    perms[rng.Intn(len(perms))],
				Share:   ShareWith(),
			}
			if i > 0 && rng.Intn(2) == 0 {
				roles[i].Share[roles[i-1].Name] = struct{}{}
				roles[i-1].Share[roles[i].Name] = struct{}{}
			}
		}

		want := bruteForce(tab, roles)
		got, err := BuildPlan(tab, roles, 99)
		if !want.feasible {
			if err == nil {
				t.Fatalf("iter %d: planner found solution but brute force says infeasible", iter)
			}
			continue
		}
		if err != nil {
			t.Fatalf("iter %d: planner failed (%v) but brute force found %v", iter, err, want.slots)
		}
		if !equalInts(got.UsedSlots, want.slots) {
			t.Fatalf("iter %d: slots %v != brute %v", iter, got.UsedSlots, want.slots)
		}
		if len(got.UsedObjects) != want.objectCount {
			t.Fatalf("iter %d: object count %d != %d", iter, len(got.UsedObjects), want.objectCount)
		}
		if err := got.Validate(tab); err != nil {
			t.Fatalf("iter %d: validate: %v\nops: %s", iter, err, dumpOps(got.Ops))
		}
		mt := NewMemTable(tab)
		if err := got.Commit(mt); err != nil {
			t.Fatalf("iter %d: commit: %v", iter, err)
		}
		assertFinalState(t, mt, tab, got)
	}
}

func assertFinalState(t *testing.T, mt *MemTable, tab *Table, p *Plan) {
	t.Helper()
	initial := tab.BySlot()
	for _, a := range p.Assignments {
		h, ok := mt.HandleAt(a.Target)
		if !ok {
			t.Fatalf("target slot %d empty", a.Target)
		}
		src := initial[a.Slot]
		if h.Object != src.Object || h.Perm != src.Perm || h.Gen != src.Gen {
			t.Fatalf("target %d holds %+v, want object %s gen %d", a.Target, h, src.Object, src.Gen)
		}
		if h.Cloexec {
			t.Fatalf("selected handle at %d is cloexec", a.Target)
		}
	}
	for _, h := range mt.Snapshot() {
		chosen := false
		for _, a := range p.Assignments {
			if a.Target == h.Slot {
				chosen = true
			}
		}
		if !chosen && !h.Cloexec {
			t.Fatalf("unselected handle at %d would be inherited", h.Slot)
		}
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func snapshotMap(mt *MemTable) map[int]Handle {
	out := map[int]Handle{}
	for _, h := range mt.Snapshot() {
		out[h.Slot] = h
	}
	return out
}

func equalHandles(a, b []Handle) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func dumpOps(ops []Op) string {
	s := ""
	for _, op := range ops {
		s += op.String() + "\n"
	}
	return s
}
