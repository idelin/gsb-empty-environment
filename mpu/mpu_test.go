package mpu

import (
	"errors"
	"math/rand"
	"testing"
)

const microSpace = uint64(128) // 微型地址空间：逐字节穷举成本极低

// expectPerms 逐地址计算区间按优先级解释后的期望权限。
func expectPerms(ivs []Interval, spaceSize int, def Perm) []Perm {
	want := make([]Perm, spaceSize)
	pri := make([]int, spaceSize)
	for i := range want {
		want[i] = def
		pri[i] = -1 << 30
	}
	for _, iv := range ivs {
		for a := int(iv.Start); a < int(iv.End); a++ {
			if iv.Priority >= pri[a] {
				want[a] = iv.Perm
				pri[a] = iv.Priority
			}
		}
	}
	return want
}

func regionsEqualMap(t *testing.T, regions []Region, want []Perm) {
	t.Helper()
	dev, err := NewDevice(uint64(len(want)), 16, regions)
	if err != nil {
		t.Fatalf("NewDevice: %v", err)
	}
	for a := range want {
		if got := dev.Effective(uint64(a)); got != want[a] {
			t.Fatalf("addr %d: effective %s, want %s (regions=%+v)",
				a, got, want[a], regions)
		}
	}
}

func TestCompileBasicAndSRD(t *testing.T) {
	ivs := []Interval{
		{Name: "ram", Start: 0, End: 32, Perm: PermR | PermW, Priority: 1},
	}
	regions, err := Compile(ivs, microSpace, PermNone, 8)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if len(regions) != 1 {
		t.Fatalf("want 1 region, got %d: %+v", len(regions), regions)
	}
	r := regions[0]
	if r.Base != 0 || r.SizeLog2 != 5 || r.SRD != 0 || r.Perm != PermR|PermW {
		t.Fatalf("unexpected region: %+v", r)
	}
	regionsEqualMap(t, regions, expectPerms(ivs, int(microSpace), PermNone))

	// 同一 32B 区域内只开放前两个 4B 子区，其余通过 SRD 屏蔽。
	ivs2 := []Interval{
		{Name: "mmio", Start: 32, End: 40, Perm: PermR, Priority: 1},
	}
	regions2, err := Compile(ivs2, microSpace, PermNone, 8)
	if err != nil {
		t.Fatalf("Compile SRD: %v", err)
	}
	found := false
	for _, r2 := range regions2 {
		if r2.Base == 32 && r2.SizeLog2 == 5 && r2.Perm == PermR {
			if r2.SRD != 0b11111100 {
				t.Fatalf("SRD = %08b, want 11111100", r2.SRD)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("expected 32B region at base 32, got %+v", regions2)
	}
	regionsEqualMap(t, regions2, expectPerms(ivs2, int(microSpace), PermNone))
}

func TestCompileFailures(t *testing.T) {
	cases := []struct {
		name string
		ivs  []Interval
		kind MapErrorKind
		wrap error
	}{
		{
			name: "subregion-misaligned",
			ivs:  []Interval{{Name: "x", Start: 2, End: 10, Perm: PermR, Priority: 1}},
			kind: MapAlignment,
			wrap: ErrNotRepresentable,
		},
		{
			name: "out-of-range",
			ivs:  []Interval{{Name: "x", Start: 0, End: 200, Perm: PermR, Priority: 1}},
			kind: MapOutOfRange,
			wrap: ErrInvalidRegion,
		},
		{
			name: "empty-interval",
			ivs:  []Interval{{Name: "x", Start: 8, End: 8, Perm: PermR, Priority: 1}},
			kind: MapOutOfRange,
			wrap: ErrInvalidRegion,
		},
		{
			name: "equal-priority-ambiguous",
			ivs: []Interval{
				{Name: "a", Start: 0, End: 16, Perm: PermR, Priority: 2},
				{Name: "b", Start: 8, End: 24, Perm: PermW, Priority: 2},
			},
			kind: MapAmbiguous,
			wrap: ErrInvalidRegion,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Compile(tc.ivs, microSpace, PermNone, 8)
			var me *MapError
			if !errors.As(err, &me) || me.Kind != tc.kind {
				t.Fatalf("want MapError kind %d, got %v", tc.kind, err)
			}
			if !errors.Is(err, tc.wrap) {
				t.Fatalf("error %v does not wrap %v", err, tc.wrap)
			}
		})
	}

	// 容量失败：8 个互不相邻的 4B 游程最少需要 4 个 32B 区域表达，
	// 把 dregion 限制为 3 必须明确失败。
	var many []Interval
	for base := 0; base < 128; base += 16 {
		many = append(many, Interval{
			Name: "s", Start: uint64(base), End: uint64(base + 4),
			Perm: PermR, Priority: 1,
		})
	}
	_, err := Compile(many, microSpace, PermNone, 3)
	var me *MapError
	if !errors.As(err, &me) || me.Kind != MapCapacity || !errors.Is(err, ErrNotRepresentable) {
		t.Fatalf("want capacity failure, got %v", err)
	}
}

func TestPriorityOverlay(t *testing.T) {
	// 低优先级大区域 RW，高优先级小区域在其中显式拒绝（PermNone）。
	regions := []Region{
		{Slot: 0, Base: 0, SizeLog2: 7, SRD: 0, Perm: PermR | PermW},
		{Slot: 1, Base: 32, SizeLog2: 5, SRD: 0, Perm: PermNone},
	}
	dev, err := NewDevice(microSpace, 8, regions)
	if err != nil {
		t.Fatalf("NewDevice: %v", err)
	}
	if dev.Effective(0) != PermR|PermW {
		t.Fatalf("low-priority region should dominate at 0")
	}
	if dev.Effective(40) != PermNone {
		t.Fatalf("high-priority PermNone region must override at 40")
	}
	if dev.Effective(100) != PermR|PermW {
		t.Fatalf("low-priority region should dominate at 100")
	}

	// Compile 端到端：同形状的区间按 Priority 编译后权限一致。
	ivs := []Interval{
		{Name: "ram", Start: 0, End: 128, Perm: PermR | PermW, Priority: 1},
		// 高优先级只读区间覆盖低优先级可写区间。
		{Name: "guard", Start: 32, End: 64, Perm: PermR, Priority: 5},
	}
	compiled, err := Compile(ivs, microSpace, PermNone, 8)
	if err != nil {
		t.Fatalf("Compile overlay: %v", err)
	}
	regionsEqualMap(t, compiled, expectPerms(ivs, int(microSpace), PermNone))
}

// checkPlanSafety 是测试侧独立复算：逐步重放，逐地址检查 W/X 不变量，
// 并确认最终状态与目标一致。
func checkPlanSafety(t *testing.T, p *Plan) {
	t.Helper()
	if err := VerifyPlan(p); err != nil {
		t.Fatalf("VerifyPlan: %v", err)
	}
	dev := replay(p.SpaceSize, p.Initial)
	initialW := replay(p.SpaceSize, p.Initial).effectiveSet(PermW)
	initialX := replay(p.SpaceSize, p.Initial).effectiveSet(PermX)
	targetDev := &Device{spaceSize: p.SpaceSize, slots: cloneSlots(p.Target)}
	targetW := targetDev.effectiveSet(PermW)
	targetX := targetDev.effectiveSet(PermX)

	raising := false
	for _, step := range p.Steps {
		if step.Kind == StepRaiseAccess {
			raising = true
		}
		dev.slots[step.Slot] = step.After
		w, x := dev.effectiveSet(PermW), dev.effectiveSet(PermX)
		if !raising {
			// 收紧阶段：W/X 必须是初始集合的子集。
			for a := 0; a < int(p.SpaceSize); a++ {
				if w.get(a) && !initialW.get(a) {
					t.Fatalf("step %+v transiently widens W at %d", step, a)
				}
				if x.get(a) && !initialX.get(a) {
					t.Fatalf("step %+v transiently widens X at %d", step, a)
				}
			}
		} else {
			// 提升阶段：W/X 必须始终是目标集合的子集。
			for a := 0; a < int(p.SpaceSize); a++ {
				if w.get(a) && !targetW.get(a) {
					t.Fatalf("step %+v grants W outside target at %d", step, a)
				}
				if x.get(a) && !targetX.get(a) {
					t.Fatalf("step %+v grants X outside target at %d", step, a)
				}
			}
		}
	}
}

func TestPlanSafeTransitions(t *testing.T) {
	initial := []Region{
		{Slot: 0, Base: 0, SizeLog2: 5, SRD: 0, Perm: PermR | PermW},
		{Slot: 1, Base: 64, SizeLog2: 5, SRD: 0, Perm: PermR | PermX},
	}
	target := []Region{
		// 槽位 0 几何搬迁到 32，且权限收紧为只读，SRD 仅开放两个子区。
		{Slot: 0, Base: 32, SizeLog2: 5, SRD: 0b11111100, Perm: PermR},
		// 槽位 1 几何不变，权限从 RX 提升为 RWX。
		{Slot: 1, Base: 64, SizeLog2: 5, SRD: 0, Perm: PermR | PermW | PermX},
		// 新启用槽位 2，默认拒绝区域。
		{Slot: 2, Base: 96, SizeLog2: 5, SRD: 0, Perm: PermNone},
	}
	dev, err := NewDevice(microSpace, 4, initial)
	if err != nil {
		t.Fatalf("NewDevice: %v", err)
	}
	plan, err := dev.PlanUpdate(target)
	if err != nil {
		t.Fatalf("PlanUpdate: %v", err)
	}
	if len(plan.Steps) == 0 {
		t.Fatal("expected register write steps")
	}
	checkPlanSafety(t, plan)

	// 目标禁用槽位 1 也必须安全。
	dev2, err := NewDevice(microSpace, 4, initial)
	if err != nil {
		t.Fatalf("NewDevice: %v", err)
	}
	plan2, err := dev2.PlanUpdate([]Region{
		{Slot: 0, Base: 0, SizeLog2: 5, SRD: 0, Perm: PermR | PermW},
	})
	if err != nil {
		t.Fatalf("PlanUpdate disable: %v", err)
	}
	checkPlanSafety(t, plan2)
}

func TestNaivePlanRejected(t *testing.T) {
	// 朴素做法：区域仍使能时直接改几何并保持 RW，必然短暂扩大 W。
	initial := []SlotState{
		{Enabled: true, Region: Region{Slot: 0, Base: 0, SizeLog2: 5, Perm: PermR | PermW}},
	}
	target := []SlotState{
		{Enabled: true, Region: Region{Slot: 0, Base: 64, SizeLog2: 5, Perm: PermR | PermW}},
	}
	naive := &Plan{
		SpaceSize: microSpace,
		Initial:   initial,
		Target:    target,
		Steps: []Step{
			{
				Kind:   StepWriteRBAR,
				Slot:   0,
				Before: initial[0],
				After:  target[0], // 使能状态下改几何，64..95 凭空变为可写
			},
		},
	}
	err := VerifyPlan(naive)
	var te *TransitionError
	if !errors.As(err, &te) || !errors.Is(err, ErrUnsafeTransition) {
		t.Fatalf("naive plan must be rejected, got %v", err)
	}
	if te.Addr < 64 || te.Addr >= 96 {
		t.Fatalf("witness addr %d outside newly writable window", te.Addr)
	}
}

// TestExhaustiveRandomPlans 在微型地址空间上随机生成可表达映射，
// 编译两端映射并规划切换，逐步穷举验证权限永不短暂扩大。
func TestExhaustiveRandomPlans(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	perms := []Perm{PermR, PermR | PermW, PermR | PermX, PermR | PermW | PermX, PermNone}
	for iter := 0; iter < 2000; iter++ {
		mkRegions := func() []Region {
			var out []Region
			used := map[int]bool{}
			slots := rng.Intn(4) + 1
			for slot := 0; slot < slots; slot++ {
				sizeLog2 := uint8(5 + rng.Intn(3)) // 32/64/128
				size := 1 << sizeLog2
				// 基址必须按区域尺寸对齐。
				base := rng.Intn(int(microSpace)/size) * size
				if base+size > int(microSpace) || used[slot] {
					continue
				}
				used[slot] = true
				srd := uint8(rng.Intn(256))
				// 至少保留一个使能子区，避免无意义区域。
				if srd == 0xff {
					srd = 0xfe
				}
				out = append(out, Region{
					Slot: slot, Base: uint64(base), SizeLog2: sizeLog2,
					SRD: srd, Perm: perms[rng.Intn(len(perms))],
				})
			}
			return out
		}
		from := mkRegions()
		to := mkRegions()
		dev, err := NewDevice(microSpace, 8, from)
		if err != nil {
			t.Fatalf("iter %d NewDevice: %v", iter, err)
		}
		plan, err := dev.PlanUpdate(to)
		if err != nil {
			// 几何/槽位约束可能使某些目标确实无安全时序；
			// 规划器必须以 ErrUnsafeTransition 明确失败，而不是产出坏计划。
			if !errors.Is(err, ErrUnsafeTransition) {
				t.Fatalf("iter %d unexpected error: %v", iter, err)
			}
			var te *TransitionError
			if !errors.As(err, &te) || te.Addr >= microSpace {
				t.Fatalf("iter %d transition error lacks witness: %v", iter, err)
			}
			continue
		}
		checkPlanSafety(t, plan)
	}
}

func TestUnsafeTransitionExplicitFailure(t *testing.T) {
	// 确定性的不可安全切换：三个槽位构成的初始映射中，带 X 的高优先级
	// 槽位 1 屏蔽了槽位 2 在 0..16 的 X；目标要求全部收紧为 PermNone。
	// 无论按什么顺序禁用，最后一个 X 槽位关闭时，X 都会短暂暴露到
	// 初始 X 集合之外，且没有空闲高优先级槽位可封锁，必须明确失败。
	initial := []Region{
		{Slot: 0, Base: 0, SizeLog2: 5, SRD: 152, Perm: PermR},
		{Slot: 1, Base: 0, SizeLog2: 5, SRD: 136, Perm: PermR | PermW | PermX},
		{Slot: 2, Base: 0, SizeLog2: 7, SRD: 13, Perm: PermR | PermW},
	}
	target := []Region{
		{Slot: 0, Base: 0, SizeLog2: 7, SRD: 150, Perm: PermNone},
		{Slot: 1, Base: 0, SizeLog2: 5, SRD: 51, Perm: PermNone},
		{Slot: 2, Base: 0, SizeLog2: 6, SRD: 193, Perm: PermNone},
	}
	dev, err := NewDevice(microSpace, 3, initial)
	if err != nil {
		t.Fatalf("NewDevice: %v", err)
	}
	_, err = dev.PlanUpdate(target)
	var te *TransitionError
	if !errors.As(err, &te) || !errors.Is(err, ErrUnsafeTransition) {
		t.Fatalf("want ErrUnsafeTransition, got %v", err)
	}
	if te.Phase != "tighten" {
		t.Fatalf("want failure in tighten phase, got phase %q", te.Phase)
	}
}
