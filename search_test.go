package netsec

import (
	"reflect"
	"testing"
)

const (
	catUser    Category = 1
	catTrusted Category = 2
	catSink    Category = 3
)

func mustSearch(t *testing.T, spec *Spec, lim Limits) SearchResult {
	t.Helper()
	res, err := Search(spec, lim)
	if err != nil {
		t.Fatalf("Search 返回错误: %v", err)
	}
	return res
}

// 案例一：类别相同却语义危险。三个片段同属一个类别，每次跳转都通过
// 类别检查，但串起来后在无能力状态下执行 readProtected。
func TestSameCategoryDangerousChain(t *testing.T) {
	spec := &Spec{
		Fragments: []Fragment{
			{ID: 0, Category: catUser, Ops: []Op{Const(0, 1)}, End: Jump(0, catUser)},
			{ID: 1, Category: catUser, Ops: []Op{Const(0, 2)}, End: Jump(0, catUser)},
			{ID: 2, Category: catUser, Ops: []Op{ReadProtected()}, End: Halt()},
		},
		Entry:        0,
		TrustedGrant: map[uint8]bool{},
	}
	res := mustSearch(t, spec, DefaultLimits())
	if res.Verdict != VerdictWitness {
		t.Fatalf("期望 VerdictWitness，得到 %v", res.Verdict)
	}
	if want := []uint8{0, 1, 2}; !reflect.DeepEqual(res.Chain, want) {
		t.Fatalf("链 = %v，期望 %v", res.Chain, want)
	}
	if res.Input.Regs != ([NumRegisters]uint8{}) || len(res.Input.Mem) != 0 {
		t.Fatalf("输入 = %+v，期望全零输入", res.Input)
	}
}

// 案例二：必须经过可信 grant。唯二到达受保护读取的路径要么被类别检查
// 拒绝，要么经过可信授予片段而获得能力，因此界内无见证。
func TestMustPassTrustedGrant(t *testing.T) {
	spec := &Spec{
		Fragments: []Fragment{
			{ID: 0, Category: catUser, End: Jump(0, catTrusted)},
			{ID: 1, Category: catTrusted, Ops: []Op{Grant(), Const(0, 2)}, End: Jump(0, catSink)},
			{ID: 2, Category: catSink, Ops: []Op{ReadProtected()}, End: Halt()},
		},
		Entry:        0,
		TrustedGrant: map[uint8]bool{1: true},
		RegChoices:   [NumRegisters][]uint8{0: {1, 2}},
	}
	res := mustSearch(t, spec, DefaultLimits())
	if res.Verdict != VerdictNotFound {
		t.Fatalf("期望 VerdictNotFound，得到 %v（链 %v）", res.Verdict, res.Chain)
	}
	if res.InputsExplored != 2 {
		t.Fatalf("枚举输入数 = %d，期望 2", res.InputsExplored)
	}
}

// 案例二的镜像：入口允许直达 sink 类别时，攻击者绕过可信 grant，
// 搜索应给出绕过授予点的字典序最小见证。
func TestBypassTrustedGrant(t *testing.T) {
	spec := &Spec{
		Fragments: []Fragment{
			{ID: 0, Category: catUser, End: Jump(0, catTrusted, catSink)},
			{ID: 1, Category: catTrusted, Ops: []Op{Grant(), Const(0, 2)}, End: Jump(0, catSink)},
			{ID: 2, Category: catSink, Ops: []Op{ReadProtected()}, End: Halt()},
		},
		Entry:        0,
		TrustedGrant: map[uint8]bool{1: true},
		RegChoices:   [NumRegisters][]uint8{0: {1, 2}},
	}
	res := mustSearch(t, spec, DefaultLimits())
	if res.Verdict != VerdictWitness {
		t.Fatalf("期望 VerdictWitness，得到 %v", res.Verdict)
	}
	if res.Input.Regs[0] != 2 {
		t.Fatalf("r0 = %d，期望 2（r0=1 经 grant 不构成漏洞）", res.Input.Regs[0])
	}
	if want := []uint8{0, 2}; !reflect.DeepEqual(res.Chain, want) {
		t.Fatalf("链 = %v，期望 %v", res.Chain, want)
	}
}

// 案例三：越类跳转被拒绝。目标片段类别不在允许集合中，路径直接被拒。
func TestCrossCategoryJumpRejected(t *testing.T) {
	spec := &Spec{
		Fragments: []Fragment{
			{ID: 0, Category: catUser, End: Jump(0, catUser)},
			{ID: 1, Category: catSink, Ops: []Op{ReadProtected()}, End: Halt()},
		},
		Entry:        0,
		TrustedGrant: map[uint8]bool{},
		RegChoices:   [NumRegisters][]uint8{0: {1}},
	}
	run := Run(spec, &Input{Regs: [NumRegisters]uint8{0: 1}}, MaxTransitionsBound)
	if run.Outcome != OutcomeRejected {
		t.Fatalf("结局 = %v，期望 OutcomeRejected", run.Outcome)
	}
	res := mustSearch(t, spec, DefaultLimits())
	if res.Verdict != VerdictNotFound {
		t.Fatalf("期望 VerdictNotFound，得到 %v", res.Verdict)
	}
}

// 案例四：写内存影响后继目标。片段把攻击者控制的寄存器存入内存，
// 再 load 回跳转寄存器，间接目标由写入的值决定。
func TestStoreMemoryAffectsJumpTarget(t *testing.T) {
	spec := &Spec{
		Fragments: []Fragment{
			{
				ID:       0,
				Category: catUser,
				Ops: []Op{
					Const(1, 5), // r1 = 地址 5
					Store(1, 2), // mem[5] = r2（攻击者控制）
					Load(0, 1),  // r0 = mem[5]
				},
				End: Jump(0, catUser),
			},
			{ID: 1, Category: catUser, Ops: []Op{ReadProtected()}, End: Halt()},
		},
		Entry:        0,
		TrustedGrant: map[uint8]bool{},
		RegChoices:   [NumRegisters][]uint8{2: {0, 1, 9}},
	}
	res := mustSearch(t, spec, DefaultLimits())
	if res.Verdict != VerdictWitness {
		t.Fatalf("期望 VerdictWitness，得到 %v", res.Verdict)
	}
	if res.Input.Regs[2] != 1 {
		t.Fatalf("r2 = %d，期望 1（0 自循环、1 才是最小见证）", res.Input.Regs[2])
	}
	if want := []uint8{0, 1}; !reflect.DeepEqual(res.Chain, want) {
		t.Fatalf("链 = %v，期望 %v", res.Chain, want)
	}

	// r2=9 时目标片段不存在，应陷阱。
	run := Run(spec, &Input{Regs: [NumRegisters]uint8{2: 9}}, MaxTransitionsBound)
	if run.Outcome != OutcomeTrap {
		t.Fatalf("r2=9 时结局 = %v，期望 OutcomeTrap", run.Outcome)
	}
	// r2=0 时自循环，应在转移上界处停止。
	run = Run(spec, &Input{}, MaxTransitionsBound)
	if run.Outcome != OutcomeBoundReached {
		t.Fatalf("r2=0 时结局 = %v，期望 OutcomeBoundReached", run.Outcome)
	}
	if len(run.Chain) != MaxTransitionsBound+1 {
		t.Fatalf("链长 = %d，期望 %d", len(run.Chain), MaxTransitionsBound+1)
	}
}

// 攻击者控制调用方指定的内存字节，直接影响跳转目标。
func TestAttackerControlledMemSlot(t *testing.T) {
	spec := &Spec{
		Fragments: []Fragment{
			{
				ID:       0,
				Category: catUser,
				Ops:      []Op{Const(1, 7), Load(0, 1)},
				End:      Jump(0, catUser),
			},
			{ID: 1, Category: catUser, Ops: []Op{ReadProtected()}, End: Halt()},
		},
		Entry:        0,
		TrustedGrant: map[uint8]bool{},
		MemSlots:     []MemSlot{{Index: 7, Choices: []uint8{0, 1}}},
	}
	res := mustSearch(t, spec, DefaultLimits())
	if res.Verdict != VerdictWitness {
		t.Fatalf("期望 VerdictWitness，得到 %v", res.Verdict)
	}
	if want := []uint8{1}; !reflect.DeepEqual(res.Input.Mem, want) {
		t.Fatalf("受控内存 = %v，期望 %v", res.Input.Mem, want)
	}
}

// 字典序最小性：多个输入都是见证时，返回字典序最小者及其链。
func TestLexicographicallySmallestWitness(t *testing.T) {
	spec := &Spec{
		Fragments: []Fragment{
			{ID: 0, Category: catUser, End: Jump(0, catUser)},
			{ID: 1, Category: catUser, Ops: []Op{ReadProtected()}, End: Halt()},
			{ID: 2, Category: catUser, Ops: []Op{ReadProtected()}, End: Halt()},
		},
		Entry:        0,
		TrustedGrant: map[uint8]bool{},
		RegChoices:   [NumRegisters][]uint8{0: {2, 0, 1}}, // 无序候选，搜索应排序
	}
	res := mustSearch(t, spec, DefaultLimits())
	if res.Verdict != VerdictWitness {
		t.Fatalf("期望 VerdictWitness，得到 %v", res.Verdict)
	}
	if res.Input.Regs[0] != 1 {
		t.Fatalf("r0 = %d，期望 1（0 自循环，1 为最小见证）", res.Input.Regs[0])
	}
	if want := []uint8{0, 1}; !reflect.DeepEqual(res.Chain, want) {
		t.Fatalf("链 = %v，期望 %v", res.Chain, want)
	}
}

// 非可信片段中的 grant 不产生能力。
func TestUntrustedGrantIgnored(t *testing.T) {
	spec := &Spec{
		Fragments: []Fragment{
			{ID: 0, Category: catUser, Ops: []Op{Grant(), ReadProtected()}, End: Halt()},
		},
		Entry:        0,
		TrustedGrant: map[uint8]bool{7: true}, // 7 不存在，0 不可信
	}
	res := mustSearch(t, spec, DefaultLimits())
	if res.Verdict != VerdictWitness {
		t.Fatalf("期望 VerdictWitness，得到 %v", res.Verdict)
	}
	if want := []uint8{0}; !reflect.DeepEqual(res.Chain, want) {
		t.Fatalf("链 = %v，期望 %v", res.Chain, want)
	}
}

// 模 256 加法与异或语义。
func TestArithmeticOps(t *testing.T) {
	spec := &Spec{
		Fragments: []Fragment{
			{
				ID:       0,
				Category: catUser,
				Ops: []Op{
					Const(0, 200),
					Const(1, 100),
					Add(0, 1), // r0 = 300 mod 256 = 44
					Xor(0, 1), // r0 = 44 ^ 100 = 72
				},
				End: Halt(),
			},
		},
		Entry:        0,
		TrustedGrant: map[uint8]bool{},
	}
	run := Run(spec, &Input{}, MaxTransitionsBound)
	if run.Outcome != OutcomeHalt {
		t.Fatalf("结局 = %v，期望 OutcomeHalt", run.Outcome)
	}
	if run.FinalRegs[0] != 72 {
		t.Fatalf("r0 = %d，期望 72", run.FinalRegs[0])
	}
}

// load/store 地址越界立即陷阱。
func TestMemoryTraps(t *testing.T) {
	loadSpec := &Spec{
		Fragments: []Fragment{
			{ID: 0, Category: catUser, Ops: []Op{Const(1, 16), Load(0, 1)}, End: Halt()},
		},
		Entry:        0,
		TrustedGrant: map[uint8]bool{},
	}
	if run := Run(loadSpec, &Input{}, MaxTransitionsBound); run.Outcome != OutcomeTrap {
		t.Fatalf("load 越界结局 = %v，期望 OutcomeTrap", run.Outcome)
	}

	storeSpec := &Spec{
		Fragments: []Fragment{
			{ID: 0, Category: catUser, Ops: []Op{Const(1, 255), Store(1, 0)}, End: Halt()},
		},
		Entry:        0,
		TrustedGrant: map[uint8]bool{},
	}
	if run := Run(storeSpec, &Input{}, MaxTransitionsBound); run.Outcome != OutcomeTrap {
		t.Fatalf("store 越界结局 = %v，期望 OutcomeTrap", run.Outcome)
	}
}

// 超出工作预算时报未知。
func TestWorkBudgetExhausted(t *testing.T) {
	spec := &Spec{
		Fragments: []Fragment{
			{ID: 0, Category: catUser, Ops: []Op{Const(0, 0), Const(1, 0), Const(2, 0)}, End: Halt()},
		},
		Entry:        0,
		TrustedGrant: map[uint8]bool{},
		RegChoices:   [NumRegisters][]uint8{0: {0, 1}},
	}
	res := mustSearch(t, spec, Limits{MaxTransitionsBound, 2})
	if res.Verdict != VerdictUnknown {
		t.Fatalf("期望 VerdictUnknown，得到 %v", res.Verdict)
	}

	// 预算充足时同一规格应正常完成。
	res = mustSearch(t, spec, Limits{MaxTransitionsBound, 100})
	if res.Verdict != VerdictNotFound {
		t.Fatalf("期望 VerdictNotFound，得到 %v", res.Verdict)
	}
	if res.InputsExplored != 2 {
		t.Fatalf("枚举输入数 = %d，期望 2", res.InputsExplored)
	}
}

// 规格校验：非法输入应报错而非默默执行。
func TestSpecValidation(t *testing.T) {
	base := func() *Spec {
		return &Spec{
			Fragments:    []Fragment{{ID: 0, Category: catUser, End: Halt()}},
			Entry:        0,
			TrustedGrant: map[uint8]bool{},
		}
	}

	spec := base()
	spec.Entry = 9
	if _, err := Search(spec, DefaultLimits()); err == nil {
		t.Fatal("入口片段不存在应报错")
	}

	spec = base()
	spec.Fragments[0].Ops = []Op{Const(0, 0), Const(0, 0), Const(0, 0), Const(0, 0), Const(0, 0)}
	if _, err := Search(spec, DefaultLimits()); err == nil {
		t.Fatal("操作数超过 4 应报错")
	}

	spec = base()
	spec.MemSlots = []MemSlot{{Index: 0, Choices: []uint8{0}}, {Index: 1, Choices: []uint8{0}}, {Index: 2, Choices: []uint8{0}}}
	if _, err := Search(spec, DefaultLimits()); err == nil {
		t.Fatal("受控内存字节超过 2 应报错")
	}

	spec = base()
	if _, err := Search(spec, Limits{MaxTransitions: 9}); err == nil {
		t.Fatal("转移上界超过 8 应报错")
	}

	spec = base()
	spec.Fragments = append(spec.Fragments, Fragment{ID: 0, Category: catUser, End: Halt()})
	if _, err := Search(spec, DefaultLimits()); err == nil {
		t.Fatal("片段 ID 重复应报错")
	}
}
