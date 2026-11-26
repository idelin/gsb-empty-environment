package dfa

import "testing"

func TestAnalyzeEmptyCandidatesOnVulnerableProgram(t *testing.T) {
	// 原程序可被单故障击穿；没有任何候选时必须报告“无可行方案”。
	prog := MustParse(`
start:
  lds r0
  jt r0, ok
  halt
ok:
  unlock
  halt
`, 0)
	rep, err := Analyze(prog, nil, nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if rep.InherentVuln != nil {
		t.Fatal("该程序无零故障漏洞，不应触发原漏洞分支")
	}
	if !rep.FeasibleNotFound || rep.Feasible != nil {
		t.Fatal("无候选时必须报告找不到可行方案")
	}
	// 空子集评估必须存在且被正确判定为不安全。
	if len(rep.Evaluations) != 1 || rep.Evaluations[0].Secure {
		t.Fatalf("空子集评估异常: %+v", rep.Evaluations)
	}
}

func TestWidthEightEnumeration(t *testing.T) {
	prog := MustParse(`
start:
  inp r0, p7
  jt r0, fire
  halt
fire:
  emit 5
  halt
`, 8)
	if got := len(AllInputs(8, false)); got != 256 {
		t.Fatalf("8 位输入应枚举 256 种，得到 %d", got)
	}
	tr, err := Run(prog, Input{Public: make([]bool, 8), SignatureValid: false})
	if err != nil {
		t.Fatal(err)
	}
	if !tr.Terminated || len(tr.Emits) != 0 {
		t.Fatalf("p7=0 时应直接 halt: %+v", tr)
	}
	bits := make([]bool, 8)
	bits[7] = true
	tr2, err := Run(prog, Input{Public: bits, SignatureValid: false})
	if err != nil {
		t.Fatal(err)
	}
	if len(tr2.Emits) != 1 || tr2.Emits[0] != 5 {
		t.Fatalf("p7=1 时应 emit 5: %+v", tr2)
	}
}

func TestStepBudgetExhaustionMarked(t *testing.T) {
	// 无条件死循环：没有 halt，64 步后必须标记 BudgetExceed 而非挂死。
	prog := MustParse(`
loop:
  jmp loop
`, 0)
	tr, err := Run(prog, Input{})
	if err != nil {
		t.Fatal(err)
	}
	if !tr.BudgetExceed || tr.Terminated {
		t.Fatalf("死循环应触发步数预算: %+v", tr)
	}
	// 攻击搜索也必须在预算内终止而不是无限展开。
	secure, err := SecureAgainstK(prog, Input{}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !secure {
		t.Fatal("纯死循环不含 unlock，应判定为安全")
	}
}

func TestOracleAndBFSAgreeOnLoop(t *testing.T) {
	// 循环 + 条件解锁：验证两份检查器在回边场景下仍一致。
	prog := MustParse(`
start:
  lds r0
  jt r0, done
  jmp start
done:
  unlock
  halt
`, 0)
	dis, err := CrossCheck(prog, Input{SignatureValid: false}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if dis != nil {
		t.Fatalf("循环程序检查器分歧: %s", dis.Reason)
	}
	att, err := Attack(prog, Input{SignatureValid: false}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !att.Reachable {
		t.Fatal("一次条件反转即可跳出循环并 unlock，攻击应可达")
	}
}

func TestSpliceRejectsOversizeResult(t *testing.T) {
	// 构造已接近 32 条上限的程序，再插入片段必须被拒绝。
	src := "start:\n"
	for i := 0; i < 30; i++ {
		src += "  inc\n"
	}
	src += "  halt\n"
	prog := MustParse(src, 0)
	_, err := Splice(prog, []Candidate{{
		ID: "x", Index: 0, Cost: 1, Gadget: TripleSigGuard([3]int{0, 1, 2}),
	}})
	if err == nil {
		t.Fatal("超过 32 条指令的拼接结果必须被链接器拒绝")
	}
}

func TestROMRangeBoundary(t *testing.T) {
	rom := []ROMRange{{Start: 2, End: 4}} // 半开区间，仅覆盖 2、3
	cases := map[int]bool{0: false, 1: false, 2: true, 3: true, 4: false}
	for idx, want := range cases {
		if got := InROM(idx, rom); got != want {
			t.Fatalf("下标 %d 的 ROM 判定=%v，期望 %v", idx, got, want)
		}
	}
}

func TestSplicePreservesOriginalControlFlow(t *testing.T) {
	// 含前向条件跳转与回边循环的程序，拼接后原跳转必须仍命中正确目标。
	prog := MustParse(`
start:
  inp r0, p0
  jt r0, body
  halt
body:
  emit 1
  lds r1
  jt r1, done
  jmp body
done:
  unlock
  emit 2
  halt
`, 1)
	patched, err := Splice(prog, []Candidate{{
		ID: "g", Index: 0, Cost: 1, Gadget: TripleSigGuard([3]int{2, 3, 4}),
	}})
	if err != nil {
		t.Fatal(err)
	}
	// 有效输入下行为必须与原程序完全一致（含回边循环只执行一次的轨迹）。
	in := Input{Public: []bool{true}, SignatureValid: true}
	orig, err := Run(prog, in)
	if err != nil {
		t.Fatal(err)
	}
	fixed, err := Run(patched, in)
	if err != nil {
		t.Fatal(err)
	}
	if orig.UnlockCount != fixed.UnlockCount || !equalInts(orig.Emits, fixed.Emits) {
		t.Fatalf("拼接破坏原控制流: orig=%+v fixed=%+v", orig, fixed)
	}
	// 验签失败时，原循环依赖 lds，修补后仍不得 unlock。
	bad := Input{Public: []bool{true}, SignatureValid: false}
	btr, err := Run(patched, bad)
	if err != nil {
		t.Fatal(err)
	}
	if btr.UnlockCount != 0 {
		t.Fatalf("验签失败不应 unlock: %+v", btr)
	}
}

func TestAnalyzeTieBreaksDeterministic(t *testing.T) {
	prog := MustParse(`
start:
  lds r0
  jt r0, ok
  halt
ok:
  unlock
  halt
`, 0)
	// 两个等价三守卫候选，成本相同，选择应按 ID 稳定确定。
	cands := []Candidate{
		{ID: "z-guard", Index: 0, Cost: 25, Gadget: TripleSigGuard([3]int{1, 2, 3})},
		{ID: "a-guard", Index: 0, Cost: 25, Gadget: TripleSigGuard([3]int{1, 2, 3})},
	}
	rep1, err := Analyze(prog, cands, nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if rep1.Feasible == nil || rep1.Feasible.Candidates[0].ID != "a-guard" {
		t.Fatalf("同成本下应确定性选择 ID 更小的方案: %+v", rep1.Feasible)
	}
	// 反向输入顺序，结论仍应一致。
	rev := []Candidate{cands[1], cands[0]}
	rep2, err := Analyze(prog, rev, nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if rep2.Feasible == nil || rep2.Feasible.Candidates[0].ID != "a-guard" {
		t.Fatalf("选择结果不应依赖候选输入顺序: %+v", rep2.Feasible)
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
