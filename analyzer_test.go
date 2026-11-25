package leaklab

import "testing"

// TestLeakyOutOfBoundsTableWitness 覆盖“存在泄漏的越界查表”：
// 分析必须找到公开相同、秘密不同、架构 probe 相同而推测 probe 不同的见证。
func TestLeakyOutOfBoundsTableWitness(t *testing.T) {
	prog := LeakyProgram(false)
	spec := LeakySpecMem()
	res, err := Analyze(Config{Program: prog, Spec: spec})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusFound || res.Witness == nil {
		t.Fatalf("status=%s reason=%s, want found with witness", res.Status, res.Reason)
	}
	w := res.Witness
	assertWitnessShape(t, w)
	if int(w.SitePC) != 3 || prog.Instr[w.SitePC].Op != OpJmpEq {
		t.Fatalf("witness site pc=%d is not the guard jmpeq", w.SitePC)
	}
	// 见证必须可独立重放并逐字节复现逐步记录。
	if err := w.Replay(); err != nil {
		t.Fatalf("witness replay failed: %v", err)
	}
	data, err := EncodeWitness(w)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeWitness(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := decoded.Replay(); err != nil {
		t.Fatalf("decoded witness replay failed: %v", err)
	}
}

// TestBarrierFixesLeak 覆盖“插入 barrier 的修复”：穷尽候选后报有界未发现。
func TestBarrierFixesLeak(t *testing.T) {
	prog := LeakyProgram(true)
	res, err := Analyze(Config{Program: prog, Spec: LeakySpecMem()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusBoundedNotFound {
		t.Fatalf("status=%s, want bounded-not-found", res.Status)
	}
	if !res.Exhausted || res.Candidates != 256*256 {
		t.Fatalf("exhausted=%t candidates=%d, want full exhaustion of %d",
			res.Exhausted, res.Candidates, 256*256)
	}
	if res.Witness != nil {
		t.Fatal("barrier must eliminate the witness")
	}
}

// TestDirectPublicLeakNotCounted 覆盖“直接公开泄密不计本题漏洞”：
// probe 仅依赖公开输入，任何秘密对的推测轨迹都相同，故有界未发现。
func TestDirectPublicLeakNotCounted(t *testing.T) {
	prog := PublicLeakProgram()
	res, err := Analyze(Config{Program: prog, Spec: LeakySpecMem()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusBoundedNotFound || !res.Exhausted {
		t.Fatalf("status=%s exhausted=%t, want bounded-not-found/exhausted", res.Status, res.Exhausted)
	}

	// 对照确认：同样的误预测下，不同秘密产生完全相同的推测轨迹。
	spec := LeakySpecMem()
	a, _ := Run(prog, spec, RunInput{Public: []uint8{OOBIndex}, Secret: 0}, RunOptions{MispredictPC: 3})
	b, _ := Run(prog, spec, RunInput{Public: []uint8{OOBIndex}, Secret: 200}, RunOptions{MispredictPC: 3})
	if !probeEqual(a.SpecTrace, b.SpecTrace) {
		t.Fatalf("public-only leak must not vary with secret: %v vs %v", a.SpecTrace, b.SpecTrace)
	}
}

// TestSecretNeverReachesArchitecturalRegisters 覆盖“回滚后秘密仍不能进入架构寄存器”。
func TestSecretNeverReachesArchitecturalRegisters(t *testing.T) {
	prog := LeakyProgram(false)
	spec := LeakySpecMem()
	base, err := Run(prog, spec, RunInput{Public: []uint8{OOBIndex}, Secret: 0}, RunOptions{MispredictPC: 3})
	if err != nil {
		t.Fatal(err)
	}
	for secret := 1; secret < 256; secret++ {
		rr, err := Run(prog, spec,
			RunInput{Public: []uint8{OOBIndex}, Secret: uint8(secret)},
			RunOptions{MispredictPC: 3})
		if err != nil {
			t.Fatal(err)
		}
		if rr.Regs != base.Regs {
			t.Fatalf("secret %d changed architectural registers: %v vs baseline %v",
				secret, rr.Regs, base.Regs)
		}
		if memExceptSlots(rr.Mem, spec) != memExceptSlots(base.Mem, spec) {
			t.Fatalf("secret %d changed architectural memory outside input slots", secret)
		}
		for _, ev := range rr.ArchTrace {
			if ev.Addr == MemSecret {
				t.Fatalf("secret slot address surfaced in architectural probe: %+v", ev)
			}
		}
	}

	// 见证两侧的最终架构寄存器/内存也必须完全一致。
	res, err := Analyze(Config{Program: prog, Spec: spec})
	if err != nil {
		t.Fatal(err)
	}
	if res.Witness == nil {
		t.Fatal("expected witness for rollback assertion")
	}
	if res.Witness.A.Result.Regs != res.Witness.B.Result.Regs {
		t.Fatalf("witness register states differ after rollback: %v vs %v",
			res.Witness.A.Result.Regs, res.Witness.B.Result.Regs)
	}
	if res.Witness.A.Result.Mem != res.Witness.B.Result.Mem {
		if memExceptSlots(res.Witness.A.Result.Mem, spec) != memExceptSlots(res.Witness.B.Result.Mem, spec) {
			t.Fatal("witness memory differs outside input slots after rollback")
		}
	}
}

// memExceptSlots 把输入槽位置零后返回数组，用于区分“输入本身”与“推测污染”。
func memExceptSlots(mem [MemSize]byte, spec InputSpec) [MemSize]byte {
	out := mem
	for _, p := range spec.Public {
		if p.Kind == SlotMem {
			out[p.Index] = 0
		}
	}
	if spec.Secret.Kind == SlotMem {
		out[spec.Secret.Index] = 0
	}
	return out
}

// TestRegisterSlotVariant 覆盖输入字节位于寄存器的布局。
func TestRegisterSlotVariant(t *testing.T) {
	prog := LeakyProgramReg(false)
	res, err := Analyze(Config{Program: prog, Spec: LeakySpecReg()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusFound || res.Witness == nil {
		t.Fatalf("register-slot variant status=%s, want found", res.Status)
	}
	if err := res.Witness.Replay(); err != nil {
		t.Fatalf("register-slot witness replay: %v", err)
	}
	// 寄存器版加 barrier 同样修复。
	fixed := AnalyzeConfig(t, LeakyProgramReg(true), LeakySpecReg())
	if fixed.Status != StatusBoundedNotFound {
		t.Fatalf("register-slot barrier fix status=%s, want bounded-not-found", fixed.Status)
	}
}

// TestBudgetThrottlingAndUnknown 验证执行次数预算限流与未知/有界未发现的边界。
func TestBudgetThrottlingAndUnknown(t *testing.T) {
	prog := LeakyProgram(false)
	spec := LeakySpecMem()

	// 预算过小：在穷尽前耗尽，必须报未知而不是有界未发现。
	res, err := Analyze(Config{Program: prog, Spec: spec, ExecBudget: 10})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusUnknown {
		t.Fatalf("status=%s, want unknown under tiny budget", res.Status)
	}
	if res.Exhausted {
		t.Fatal("partial enumeration must not be marked exhausted")
	}
	if res.Executions > 10 {
		t.Fatalf("executions=%d exceeded budget 10", res.Executions)
	}

	// 预算充足时应能找到见证，且执行数不超过预算。
	full, err := Analyze(Config{Program: prog, Spec: spec, ExecBudget: 20000})
	if err != nil {
		t.Fatal(err)
	}
	if full.Status != StatusFound {
		t.Fatalf("status=%s, want found within generous budget", full.Status)
	}
	if full.Executions > 20000 {
		t.Fatalf("executions=%d exceeded budget", full.Executions)
	}
}

// TestTwoPublicInputs 覆盖至多两个公开字节的枚举与配对路径。
func TestTwoPublicInputs(t *testing.T) {
	spec := InputSpec{
		Public: []Slot{
			{Kind: SlotMem, Index: MemPublic},
			{Kind: SlotReg, Index: RZero}, // 恒零槽，不参与语义
		},
		Secret: Slot{Kind: SlotMem, Index: MemSecret},
	}
	prog := LeakyProgram(false)
	res, err := Analyze(Config{Program: prog, Spec: spec, ExecBudget: 200000})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusFound || res.Witness == nil {
		t.Fatalf("two-public-input status=%s reason=%s", res.Status, res.Reason)
	}
	if len(res.Witness.A.Input.Public) != 2 {
		t.Fatalf("witness public input width = %d, want 2", len(res.Witness.A.Input.Public))
	}
	if err := res.Witness.Replay(); err != nil {
		t.Fatal(err)
	}
}

// TestOutOfBoundsAddressing 显式验证“越界查表”语义：公开下标 20 已越过
// 16 项表边界（合法 0..15），推测地址 = 20 + 秘密，probe 桶随秘密变化。
func TestOutOfBoundsAddressing(t *testing.T) {
	prog := LeakyProgram(false)
	spec := LeakySpecMem()
	if OOBIndex < 16 {
		t.Fatalf("OOBIndex %d must be outside the 16-entry table", OOBIndex)
	}
	seen := map[uint8]bool{}
	for _, secret := range []uint8{0, 15, 16, 200} {
		rr, err := Run(prog, spec,
			RunInput{Public: []uint8{OOBIndex}, Secret: secret},
			RunOptions{MispredictPC: 3})
		if err != nil {
			t.Fatal(err)
		}
		if len(rr.SpecTrace) != 1 {
			t.Fatalf("secret %d: expected one spec probe, got %+v", secret, rr.SpecTrace)
		}
		ev := rr.SpecTrace[0]
		wantAddr := OOBIndex + secret // 8 位自然回绕
		if ev.Addr != wantAddr {
			t.Errorf("secret %d: spec addr=%d want %d", secret, ev.Addr, wantAddr)
		}
		if ev.Set != wantAddr/16 {
			t.Errorf("secret %d: probe set=%d want %d", secret, ev.Set, wantAddr/16)
		}
		seen[ev.Set] = true
		// 架构侧不得留下任何 probe。
		if len(rr.ArchTrace) != 0 {
			t.Fatalf("secret %d: architectural probe leaked: %+v", secret, rr.ArchTrace)
		}
	}
	if len(seen) < 2 {
		t.Fatalf("probe set must vary with secret, saw %v", seen)
	}
}

func assertWitnessShape(t *testing.T, w *Witness) {
	t.Helper()
	if !equalUint8(w.A.Input.Public, w.B.Input.Public) {
		t.Fatalf("public inputs differ: %v vs %v", w.A.Input.Public, w.B.Input.Public)
	}
	if w.A.Input.Secret == w.B.Input.Secret {
		t.Fatal("witness secrets must differ")
	}
	if w.A.MispredictPC != w.B.MispredictPC || w.A.MispredictPC != int(w.SitePC) {
		t.Fatalf("mispredict sites disagree: A=%d B=%d site=%d",
			w.A.MispredictPC, w.B.MispredictPC, w.SitePC)
	}
	if !probeEqual(w.A.Result.ArchTrace, w.B.Result.ArchTrace) {
		t.Fatal("architectural probe traces must be equal")
	}
	if probeEqual(w.A.Result.SpecTrace, w.B.Result.SpecTrace) {
		t.Fatal("speculative probe traces must differ")
	}
	if len(w.A.Result.SpecTrace) == 0 || len(w.B.Result.SpecTrace) == 0 {
		t.Fatal("a speculative-only leak needs non-empty speculative traces")
	}
}

func equalUint8(a, b []uint8) bool {
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

func AnalyzeConfig(t *testing.T, prog *Program, spec InputSpec) *AnalysisResult {
	t.Helper()
	res, err := Analyze(Config{Program: prog, Spec: spec})
	if err != nil {
		t.Fatal(err)
	}
	return res
}
