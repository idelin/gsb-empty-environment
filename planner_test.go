package netsec

import "testing"

// simulateWrites replays a plan through the exported Apply semantics and
// asserts the forbidden invariant at every step.
func simulateWrites(t *testing.T, m Model, writes []Write) uint32 {
	t.Helper()
	state := m.Start
	for _, w := range writes {
		state = Apply(state, w, m.Regions)
		if !m.Safe(state) {
			t.Fatalf("write %v opened forbidden region: state=%b", w, state)
		}
	}
	return state
}

func TestApplyLockIsStickyAndEnableFreezes(t *testing.T) {
	var state uint32

	state = Apply(state, Write{WriteMask: 1, Value: 1}, 1)
	if state != 1 {
		t.Fatalf("enable set: got %b", state)
	}

	state = Apply(state, Write{WriteMask: 2, Value: 2}, 1)
	if state != 3 {
		t.Fatalf("lock: got %b", state)
	}

	state = Apply(state, Write{WriteMask: 3, Value: 0}, 1)
	if state != 3 {
		t.Fatalf("locked state changed: got %b", state)
	}

	if got := Apply(0, Write{WriteMask: 2, Value: 0}, 1); got != 0 {
		t.Fatalf("lock write with value 0 must be no-op, got %b", got)
	}
}

func TestPlanLocksWithoutOpeningForbidden(t *testing.T) {
	regions := []RegionSpec{
		{Name: "data", StartEnable: true, TargetEnable: false, TargetLock: true},
		{Name: "secret", Forbidden: true, TargetLock: true},
	}
	m, err := BuildModel(regions, false)
	if err != nil {
		t.Fatal(err)
	}
	p := PlanModel(m, Budget{})
	if p.Status != StatusSolved {
		t.Fatalf("status=%s err=%v", p.Status, p.Err)
	}

	final := simulateWrites(t, m, p.Writes)
	if !m.AtGoal(final) {
		t.Fatalf("final state %b misses target %b mask %b", final, m.Target, m.TargetMask)
	}
	if final != 0b1010 {
		t.Fatalf("final=%b want=1010", final)
	}
	if p.Depth != 1 {
		t.Fatalf("depth=%d want 1", p.Depth)
	}

	annotated := Annotate(m, p)
	if len(annotated) != 1 {
		t.Fatalf("unexpected annotation: %+v", annotated)
	}
}

func TestPlanBatchesRegionsInOneWrite(t *testing.T) {
	regions := []RegionSpec{
		{TargetEnable: true, TargetLock: true},
		{TargetEnable: true, TargetLock: true},
	}
	m, err := BuildModel(regions, false)
	if err != nil {
		t.Fatal(err)
	}
	p := PlanModel(m, Budget{})
	if p.Status != StatusSolved || p.Depth != 1 {
		t.Fatalf("status=%s depth=%d", p.Status, p.Depth)
	}
	final := simulateWrites(t, m, p.Writes)
	if final != 0b1111 {
		t.Fatalf("final=%b", final)
	}
}

func TestSingleFieldRequiresMoreWrites(t *testing.T) {
	regions := []RegionSpec{
		{TargetEnable: true, TargetLock: true},
		{TargetEnable: true, TargetLock: true},
	}
	m, err := BuildModel(regions, true)
	if err != nil {
		t.Fatal(err)
	}
	p := PlanModel(m, Budget{})
	if p.Status != StatusSolved {
		t.Fatalf("status=%s err=%v", p.Status, p.Err)
	}
	if p.Depth != 2 {
		t.Fatalf("single-field depth=%d want 2", p.Depth)
	}
	for _, w := range p.Writes {
		touched := 0
		for r := 0; r < m.Regions; r++ {
			regionMask := uint32(1)<<EnableBit(r) | uint32(1)<<LockBit(r)
			if w.WriteMask&regionMask != 0 {
				touched++
			}
		}
		if touched > 1 {
			t.Fatalf("write %v touches %d regions", w, touched)
		}
	}
	simulateWrites(t, m, p.Writes)
}

func TestStartAlreadyForbiddenIsUnreachable(t *testing.T) {
	m := Model{
		Regions:       1,
		Start:         1,
		Target:        2,
		TargetMask:    2,
		ForbiddenMask: 1,
	}
	if p := PlanModel(m, Budget{}); p.Status != StatusUnreachable {
		t.Fatalf("status=%s want unreachable", p.Status)
	}
}

func TestLockedWrongEnableIsUnrecoverable(t *testing.T) {
	m := Model{
		Regions:    1,
		Start:      0b11,
		Target:     0b10,
		TargetMask: 0b11,
	}
	if p := PlanModel(m, Budget{}); p.Status != StatusUnreachable {
		t.Fatalf("status=%s want unreachable", p.Status)
	}
}

func TestZeroWriteWhenStartMeetsGoal(t *testing.T) {
	m := Model{Regions: 1, Start: 0b10, Target: 0b10, TargetMask: 0b11}
	p := PlanModel(m, Budget{})
	if p.Status != StatusSolved || p.Depth != 0 || len(p.Writes) != 0 {
		t.Fatalf("unexpected plan: %+v", p)
	}
	if p.Writes == nil {
		t.Fatal("Writes should be non-nil empty slice")
	}
}

func TestBudgetUnfinished(t *testing.T) {
	regions := []RegionSpec{
		{TargetEnable: true, TargetLock: true},
		{TargetEnable: true, TargetLock: true},
	}
	m, err := BuildModel(regions, true)
	if err != nil {
		t.Fatal(err)
	}
	if p := PlanModel(m, Budget{MaxWrites: 1}); p.Status != StatusUnfinished {
		t.Fatalf("status=%s want unfinished", p.Status)
	}
	if p := PlanModel(m, Budget{MaxVisited: 1}); p.Status != StatusUnfinished {
		t.Fatalf("status=%s want unfinished on visit budget", p.Status)
	}
}

func TestInvalidModels(t *testing.T) {
	cases := []Model{
		{Regions: 0},
		{Regions: 13},
		{Regions: 1, Start: 1 << 2},
		{Regions: 1, Target: 1, TargetMask: 0},
		{Regions: 1, ForbiddenMask: 2},
		{Regions: 1, Target: 1, TargetMask: 1, ForbiddenMask: 1},
	}
	for idx, m := range cases {
		if err := m.Validate(); err == nil {
			t.Fatalf("case %d: expected validation error", idx)
		}
		if p := PlanModel(m, Budget{}); p.Status != StatusInvalid {
			t.Fatalf("case %d: status=%s want invalid", idx, p.Status)
		}
	}
}

func TestTargetMaskDontCare(t *testing.T) {
	m := Model{
		Regions:    2,
		Target:     2,
		TargetMask: 2,
	}
	p := PlanModel(m, Budget{})
	if p.Status != StatusSolved || p.Depth != 1 {
		t.Fatalf("status=%s depth=%d", p.Status, p.Depth)
	}
	final := simulateWrites(t, m, p.Writes)
	if final&2 != 2 {
		t.Fatalf("region 0 not locked: %b", final)
	}
}
