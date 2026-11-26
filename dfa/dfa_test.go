package dfa

import (
	"reflect"
	"testing"
)

// 安全启动示例程序（有效验签才 unlock，并发出 emit 事件）：
//
//	start: lds r0          # r0 = 验签结果位（只读）
//	       jt r0, ok
//	       halt
//	ok:    emit 3
//	       unlock
//	       emit 7
//	       halt
const bootAsm = `
start:
  lds r0
  jt r0, ok
  halt
ok:
  emit 3
  unlock
  emit 7
  halt
`

func bootProgram(t *testing.T) *Program {
	t.Helper()
	prog := MustParse(bootAsm, 0)
	if len(prog.Instrs) != 7 {
		t.Fatalf("指令数=%d，期望 7", len(prog.Instrs))
	}
	return prog
}

func TestBaselineRun(t *testing.T) {
	prog := bootProgram(t)

	bad, err := Run(prog, Input{Public: nil, SignatureValid: false})
	if err != nil {
		t.Fatal(err)
	}
	if bad.UnlockCount != 0 || bad.Terminated != true || len(bad.Emits) != 0 {
		t.Fatalf("验签失败基线行为异常: %+v", bad)
	}

	good, err := Run(prog, Input{Public: nil, SignatureValid: true})
	if err != nil {
		t.Fatal(err)
	}
	if good.UnlockCount != 1 || !reflect.DeepEqual(good.Emits, []int{3, 7}) {
		t.Fatalf("验签成功基线行为异常: %+v", good)
	}
}

func TestLinkerRejectsOversizedProgram(t *testing.T) {
	src := ""
	for i := 0; i < MaxInstrs+1; i++ {
		src += "halt\n"
	}
	if _, err := ParseText(src); err != nil {
		t.Fatalf("解析阶段不应报错: %v", err)
	} else if _, err := MustParseText(src, 0); err == nil {
		t.Fatal("超过 32 条指令必须被链接器拒绝")
	}
}

// MustParseText 暴露给测试的两阶段构造。
func MustParseText(src string, width int) (*Program, error) {
	pt, err := ParseText(src)
	if err != nil {
		return nil, err
	}
	return pt.Link(width)
}

func TestSingleGuardFallsToDoubleInvert(t *testing.T) {
	prog := bootProgram(t)

	// 在入口插入一个单守卫。该守卫本身可被一次条件反转击穿，
	// 原程序主守卫再被一次条件反转击穿——双跳即可 unlock。
	cands := []Candidate{{
		ID: "single", Index: 0, Cost: 10, Gadget: SigGuard(1),
	}}
	patched, err := Splice(prog, cands)
	if err != nil {
		t.Fatal(err)
	}

	// K=1 仍然安全：单故障无法同时越过两个守卫。
	secure1, err := SecureAgainstK(patched, Input{SignatureValid: false}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !secure1 {
		t.Fatal("单故障不应击穿单守卫 + 主守卫的组合")
	}

	// K=2 必被击穿。BFS 按枚举顺序优先给出寄存器翻转见证。
	att, err := Attack(patched, Input{SignatureValid: false}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !att.Reachable || att.FaultsUsed > 2 {
		t.Fatalf("期望双故障可达，得到 %+v", att)
	}

	// 见证必须能被参考 VM 独立重放出 unlock。
	tr, err := Replay(patched, Input{SignatureValid: false}, att.Witness)
	if err != nil {
		t.Fatal(err)
	}
	if tr.UnlockCount == 0 {
		t.Fatal("攻击见证重放未观察到 unlock")
	}

	// 显式构造“双跳”攻击：两次条件反转分别击穿插入守卫与原主守卫。
	// 步号按实际执行步计数：守卫 jt 在步 1；反转后越过其 halt，
	// 原程序主 jt 在步 3。
	doubleJump := []Fault{
		{Step: 1, Kind: FaultInvert},
		{Step: 3, Kind: FaultInvert},
	}
	djTr, err := Replay(patched, Input{SignatureValid: false}, doubleJump)
	if err != nil {
		t.Fatal(err)
	}
	if djTr.UnlockCount == 0 {
		t.Fatal("双条件反转（双跳）必须能击穿单守卫并到达 unlock")
	}
}

func TestRegisterFlipReachesUnlock(t *testing.T) {
	prog := bootProgram(t)
	// 不插任何守卫，仅针对原程序：在 jt 读取 r0 前翻转 r0 即可越权。
	att, err := Attack(prog, Input{SignatureValid: false}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !att.Reachable {
		t.Fatal("寄存器翻转攻击应可达 unlock")
	}
	flips := 0
	for _, f := range att.Witness {
		if f.Kind == FaultFlip && f.Reg == 0 {
			flips++
		}
	}
	if flips == 0 {
		t.Fatalf("期望至少一次 r0 翻转，得到 %+v", att.Witness)
	}
}

func TestTripleGuardResistsTwoFaults(t *testing.T) {
	prog := bootProgram(t)
	cands := []Candidate{{
		ID: "triple", Index: 0, Cost: 25,
		Gadget: TripleSigGuard([3]int{1, 2, 3}),
	}}
	patched, err := Splice(prog, cands)
	if err != nil {
		t.Fatal(err)
	}
	if len(patched.Instrs) > MaxInstrs {
		t.Fatalf("修补后指令数 %d 超限", len(patched.Instrs))
	}
	secure, err := SecureAgainstK(patched, Input{SignatureValid: false}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !secure {
		t.Fatal("三守卫必须抵御 K=2 双故障")
	}

	// 有效输入下 unlock 次数与 emit 序列必须与原程序完全一致。
	orig, _ := Run(prog, Input{SignatureValid: true})
	fixed, _ := Run(patched, Input{SignatureValid: true})
	if orig.UnlockCount != fixed.UnlockCount || !reflect.DeepEqual(orig.Emits, fixed.Emits) {
		t.Fatalf("有效输入可观察行为不一致: orig=%+v fixed=%+v", orig, fixed)
	}
}

func TestCounterGateSemanticsAndKnownWeakness(t *testing.T) {
	prog := bootProgram(t)
	gate, err := CounterGate(3)
	if err != nil {
		t.Fatal(err)
	}
	patched, err := Splice(prog, []Candidate{{ID: "gate", Index: 0, Cost: 30, Gadget: gate}})
	if err != nil {
		t.Fatal(err)
	}
	// 无故障时，验签失败输入被原程序挡在 unlock 之前。
	base, err := Run(patched, Input{SignatureValid: false})
	if err != nil {
		t.Fatal(err)
	}
	if base.UnlockCount != 0 {
		t.Fatal("无故障下计数器片段不应放行")
	}
	// 单故障即可击穿共享计数器（一次 jz 反转），组件如实暴露该边界。
	att, err := Attack(patched, Input{SignatureValid: false}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !att.Reachable {
		t.Fatal("共享计数器应在 K=1 下被击穿，这是 CounterGate 的已知边界")
	}
	// 计数器在有效路径上必须被恢复为 0。
	tr, err := Run(patched, Input{SignatureValid: true})
	if err != nil {
		t.Fatal(err)
	}
	if tr.LastState.Counter != 0 || tr.UnlockCount != 1 {
		t.Fatalf("计数器未恢复或 unlock 次数错误: %+v", tr)
	}
	if !reflect.DeepEqual(tr.Emits, []int{3, 7}) {
		t.Fatalf("计数器片段污染了 emit 序列: %+v", tr.Emits)
	}
}

func TestAnalyzePicksCheapestFeasibleAndHonorsROM(t *testing.T) {
	prog := bootProgram(t)
	cands := []Candidate{
		{ID: "cheap-single", Index: 0, Cost: 10, Gadget: SigGuard(1)},
		{ID: "rom-single", Index: 1, Cost: 1, Gadget: SigGuard(2)},
		{ID: "triple-a", Index: 0, Cost: 25, Gadget: TripleSigGuard([3]int{1, 2, 3})},
		{ID: "triple-b", Index: 4, Cost: 40, Gadget: TripleSigGuard([3]int{1, 2, 3})},
	}
	rom := []ROMRange{{Start: 1, End: 3}} // 原程序下标 1、2 为只读

	rep, err := Analyze(prog, cands, rom, 2)
	if err != nil {
		t.Fatal(err)
	}
	if rep.InherentVuln != nil {
		t.Fatal("示例程序不存在无故障原漏洞")
	}
	if len(rep.Excluded) != 1 || rep.Excluded[0].Candidate.ID != "rom-single" {
		t.Fatalf("ROM 排除结果错误: %+v", rep.Excluded)
	}
	if rep.FeasibleNotFound || rep.Feasible == nil {
		t.Fatal("应当找到可行修补方案")
	}
	if len(rep.Feasible.Candidates) != 1 || rep.Feasible.Candidates[0].ID != "triple-a" {
		t.Fatalf("最小代价可行方案应为 triple-a，得到 %+v", rep.Feasible.Candidates)
	}
	if rep.Feasible.TotalCost != 25 {
		t.Fatalf("总代价=%d，期望 25", rep.Feasible.TotalCost)
	}
	if !rep.Feasible.CrossCheckOK {
		t.Fatalf("交叉对照失败: %s", rep.Feasible.CrossCheckNote)
	}

	// 单守卫方案必须出现在评估记录里且被标记为不安全。
	foundSingle := false
	for _, ev := range rep.Evaluations {
		if len(ev.Candidates) == 1 && ev.Candidates[0].ID == "cheap-single" {
			foundSingle = true
			if ev.Secure {
				t.Fatal("单守卫在 K=2 下不能被判定为安全")
			}
		}
	}
	if !foundSingle {
		t.Fatal("评估记录缺少单守卫方案")
	}
}

func TestInherentVulnerabilityRejected(t *testing.T) {
	// 无论验签结果如何都 unlock 的程序属于无故障原漏洞。
	bad := MustParse("unlock\nhalt\n", 0)
	rep, err := Analyze(bad, nil, nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if rep.InherentVuln == nil {
		t.Fatal("必须识别出无故障原漏洞")
	}
	if rep.Feasible != nil {
		t.Fatal("存在原漏洞时不得给出修补方案")
	}
}

func TestEmitSequenceMismatchDetected(t *testing.T) {
	prog := bootProgram(t)
	// 片段在有效路径上额外 emit 一个 tag，破坏事件序列，应被等价性检查拒绝。
	noisy := Gadget{
		Name: "noisy",
		Source: `
  emit 9
  jmp g_ok
g_ok:
`,
		MinFault: 0,
	}
	patched, err := Splice(prog, []Candidate{{ID: "noisy", Index: 0, Cost: 5, Gadget: noisy}})
	if err != nil {
		t.Fatal(err)
	}
	orig, _ := Run(prog, Input{SignatureValid: true})
	fixed, _ := Run(patched, Input{SignatureValid: true})
	if tracesEquivalent(orig, fixed) {
		t.Fatal("额外 emit 必须被识别为序列不一致")
	}
}

func TestOracleAgreesWithBFS(t *testing.T) {
	prog := bootProgram(t)
	single, err := Splice(prog, []Candidate{{ID: "s", Index: 0, Cost: 1, Gadget: SigGuard(1)}})
	if err != nil {
		t.Fatal(err)
	}
	triple, err := Splice(prog, []Candidate{{
		ID: "t", Index: 0, Cost: 1, Gadget: TripleSigGuard([3]int{1, 2, 3}),
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		prog *Program
		want bool
	}{
		{"single-guard-broken", single, true},
		{"triple-guard-safe", triple, false},
		{"baseline-broken", prog, true},
	} {
		in := Input{SignatureValid: false}
		dis, err := CrossCheck(tc.prog, in, 2)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if dis != nil {
			t.Fatalf("%s: 检查器分歧: %s", tc.name, dis.Reason)
		}
		orc, err := ExhaustiveOracle(tc.prog, in, 2)
		if err != nil {
			t.Fatal(err)
		}
		if orc.Reachable != tc.want {
			t.Fatalf("%s: 预言机可达=%v，期望 %v", tc.name, orc.Reachable, tc.want)
		}
	}
}

func TestHaltCannotBeSkipped(t *testing.T) {
	prog := MustParse(`halt`, 0)
	_, err := Replay(prog, Input{}, []Fault{{Step: 0, Kind: FaultSkip}})
	if err == nil {
		t.Fatal("halt 上的 skip 故障必须被拒绝")
	}
}

func TestCounterWrapsModulo16(t *testing.T) {
	// 连续 16 次 inc 后计数器应回到 0，jz 命中。
	src := `
start:
  inc
  jz done
  jmp start
done:
  emit 1
  halt
`
	prog := MustParse(src, 0)
	tr, err := Run(prog, Input{})
	if err != nil {
		t.Fatal(err)
	}
	if !tr.Terminated || !reflect.DeepEqual(tr.Emits, []int{1}) || tr.LastState.Counter != 0 {
		t.Fatalf("计数器模 16 回绕错误: %+v", tr)
	}
}
