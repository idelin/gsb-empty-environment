package leaklab

import "testing"

func mustProg(t *testing.T, text string) *Program {
	t.Helper()
	prog, err := ParseProgram(text)
	if err != nil {
		t.Fatalf("parse program: %v\nprogram:\n%s", err, text)
	}
	return prog
}

func TestArithmeticAndLoad(t *testing.T) {
	prog := mustProg(t, `
		const r0, 250
		const r1, 10
		add r2, r0, r1        # 250 + 10 = 4 mod 256
		xor r3, r2, r1        # 4 ^ 10 = 14
		const r4, 7
		store_probe:
		probe r4             # 记录桶 7/16 = 0
		load r5, r4          # r5 = mem[7] = 0（初值固定为零）
		halt
	`)
	spec := InputSpec{Secret: Slot{Kind: SlotMem, Index: 0}}
	rr, err := Run(prog, spec, RunInput{Secret: 0}, RunOptions{MispredictPC: -1})
	if err != nil {
		t.Fatal(err)
	}
	if rr.Status != StatusHalt {
		t.Fatalf("status = %s, want halt", rr.Status)
	}
	if rr.Regs[2] != 4 {
		t.Errorf("add mod256: r2=%d want 4", rr.Regs[2])
	}
	if rr.Regs[3] != 14 {
		t.Errorf("xor: r3=%d want 14", rr.Regs[3])
	}
	if rr.Regs[5] != 0 {
		t.Errorf("load zeroed mem: r5=%d want 0", rr.Regs[5])
	}
	if len(rr.ArchTrace) != 1 || rr.ArchTrace[0].Set != 0 || rr.ArchTrace[0].Addr != 7 {
		t.Errorf("probe event = %+v, want set 0 addr 7", rr.ArchTrace)
	}
}

func TestInputSlotsAndOverlapValidation(t *testing.T) {
	spec := InputSpec{
		Public: []Slot{{Kind: SlotReg, Index: 0}, {Kind: SlotMem, Index: 10}},
		Secret: Slot{Kind: SlotMem, Index: 10},
	}
	if err := spec.Validate(); err == nil {
		t.Fatal("expected overlap error for public/secret sharing mem[10]")
	}
	spec.Secret = Slot{Kind: SlotMem, Index: 11}
	if err := spec.Validate(); err != nil {
		t.Fatalf("valid spec rejected: %v", err)
	}

	prog := mustProg(t, "const r1, 10\nload r2, r1\nhalt")
	rr, err := Run(prog, spec, RunInput{Public: []uint8{9, 77}, Secret: 3}, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rr.Regs[0] != 9 {
		t.Errorf("register public slot: r0=%d want 9", rr.Regs[0])
	}
	if rr.Mem[10] != 77 {
		t.Errorf("memory public slot: mem[10]=%d want 77", rr.Mem[10])
	}
	if rr.Mem[11] != 3 {
		t.Errorf("memory secret slot: mem[11]=%d want 3", rr.Mem[11])
	}
	if rr.Regs[2] != 77 {
		t.Errorf("load from mem[10]: r2=%d want 77", rr.Regs[2])
	}
}

func TestStepBudgetUnknown(t *testing.T) {
	// r0 恒等于自身，自循环跳转永不 halt，超过 64 步即未知。
	prog := mustProg(t, "loop:\njmpeq r0, r0, loop\nhalt")
	spec := InputSpec{Secret: Slot{Kind: SlotReg, Index: 1}}
	rr, err := Run(prog, spec, RunInput{Secret: 0}, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rr.Status != StatusStepBudget || rr.ArchSteps != MaxArchSteps {
		t.Fatalf("status=%s steps=%d, want step-budget/%d", rr.Status, rr.ArchSteps, MaxArchSteps)
	}
}

func TestSpeculationRollbackAndProbeRetention(t *testing.T) {
	// r1 初始为秘密 5；r0=0 时守卫为假，架构 fallthrough halt。
	// 误预测让推测窗口跳到 leak 块改写 r2/r3 并 probe，回滚后架构状态必须干净。
	prog := mustProg(t, `
		jmpeq r0, r1, leak
		halt
		leak:
		const r2, 200
		xor r3, r1, r1
		probe r2
		jmpeq r0, r0, safe
		safe:
		halt
	`)
	spec := InputSpec{Secret: Slot{Kind: SlotReg, Index: 1}}

	rr, err := Run(prog, spec, RunInput{Secret: 5}, RunOptions{MispredictPC: 0})
	if err != nil {
		t.Fatal(err)
	}
	if !rr.Mispredicted || rr.MispredictPC != 0 {
		t.Fatalf("misprediction not recorded: mis=%d flag=%t", rr.MispredictPC, rr.Mispredicted)
	}
	if len(rr.SpecTrace) != 1 || rr.SpecTrace[0].Addr != 200 || rr.SpecTrace[0].Set != 12 {
		t.Fatalf("spec probe retained = %+v", rr.SpecTrace)
	}
	if len(rr.ArchTrace) != 0 {
		t.Fatalf("architectural trace must stay empty, got %+v", rr.ArchTrace)
	}
	// 回滚后推测写入的 r2/r3 不得进入架构状态，秘密也不得扩散。
	if rr.Regs[2] != 0 || rr.Regs[3] != 0 {
		t.Errorf("rollback failed: r2=%d r3=%d want 0,0", rr.Regs[2], rr.Regs[3])
	}
	if rr.Regs[0] != 0 {
		t.Errorf("r0 mutated by speculation: %d", rr.Regs[0])
	}
	// 逐步记录必须包含 spec 与 spec-end 标记且顺序正确。
	var sawSpec, sawEnd bool
	for _, s := range rr.Steps {
		if s.Kind == StepSpec {
			sawSpec = true
		}
		if s.Kind == StepSpecEnd {
			sawEnd = true
		}
	}
	if !sawSpec || !sawEnd {
		t.Fatalf("missing spec/end markers in steps: %+v", rr.Steps)
	}
}

func TestBarrierStopsSpeculation(t *testing.T) {
	prog := mustProg(t, `
		jmpeq r0, r1, leak
		halt
		leak:
		barrier
		const r2, 99
		probe r2
		jmpeq r0, r0, safe
		safe:
		halt
	`)
	spec := InputSpec{Secret: Slot{Kind: SlotReg, Index: 1}}
	rr, err := Run(prog, spec, RunInput{Secret: 5}, RunOptions{MispredictPC: 0})
	if err != nil {
		t.Fatal(err)
	}
	if len(rr.SpecTrace) != 0 {
		t.Fatalf("barrier must suppress spec probes, got %+v", rr.SpecTrace)
	}
	end := findSpecEnd(t, rr)
	if end.Kind != StepSpecEnd || end.Note != "barrier" {
		t.Fatalf("expected barrier spec-end, got %+v", end)
	}
	if rr.Regs[2] != 0 {
		t.Errorf("r2 changed despite barrier: %d", rr.Regs[2])
	}
}

func TestSpecWindowLimitedToEightSteps(t *testing.T) {
	// 推测块为 8 条 const，窗口用尽后结束，不得执行第 9 条 probe。
	prog := mustProg(t, `
		jmpeq r0, r1, leak
		halt
		leak:
		const r2, 1
		const r2, 2
		const r2, 3
		const r2, 4
		const r2, 5
		const r2, 6
		const r2, 7
		const r2, 8
		probe r2
		jmpeq r0, r0, safe
		safe:
		halt
	`)
	spec := InputSpec{Secret: Slot{Kind: SlotReg, Index: 1}}
	rr, err := Run(prog, spec, RunInput{Secret: 5}, RunOptions{MispredictPC: 0})
	if err != nil {
		t.Fatal(err)
	}
	if len(rr.SpecTrace) != 0 {
		t.Fatalf("probe beyond 8-step window leaked: %+v", rr.SpecTrace)
	}
	end := findSpecEnd(t, rr)
	if end.Note != "spec-window" {
		t.Fatalf("expected spec-window end note, got %+v", end)
	}
	if rr.Regs[2] != 0 {
		t.Errorf("rollback after window failed: r2=%d", rr.Regs[2])
	}
}

func TestSingleMispredictionPerRun(t *testing.T) {
	// 两个连续条件跳转都给了同一误预测 PC 选项，只有第一次真正反转。
	prog := mustProg(t, `
		jmpeq r0, r1, l1
		l1:
		jmpeq r0, r1, l2
		l2:
		halt
	`)
	spec := InputSpec{Secret: Slot{Kind: SlotReg, Index: 1}}
	rr, err := Run(prog, spec, RunInput{Secret: 5}, RunOptions{MispredictPC: 0})
	if err != nil {
		t.Fatal(err)
	}
	misCount := 0
	for _, s := range rr.Steps {
		if s.Mispredict {
			misCount++
		}
	}
	if misCount != 1 {
		t.Fatalf("expected exactly one misprediction, got %d", misCount)
	}
}

func findSpecEnd(t *testing.T, rr *RunResult) StepRecord {
	t.Helper()
	for i := len(rr.Steps) - 1; i >= 0; i-- {
		if rr.Steps[i].Kind == StepSpecEnd {
			return rr.Steps[i]
		}
	}
	t.Fatalf("no spec-end record in steps: %+v", rr.Steps)
	return StepRecord{}
}
