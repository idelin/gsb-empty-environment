// 双故障分析与修补组件的端到端演示，宿主可直接 `go run ./cmd/demo`。
package main

import (
	"fmt"

	"formal.local/h26-805a940eb5/dfa"
)

const secureBoot = `
start:
  inp r0, p0
  lds r1
  and r0, r1
  jt r0, ok
  emit 0
  halt
ok:
  emit 3
  unlock
  emit 7
  halt
`

func main() {
	prog := dfa.MustParse(secureBoot, 1)
	fmt.Println("==== 原程序（含公开位 p0 与只读验签位）====")
	fmt.Println(prog.Disassemble())

	// 1) 无故障原漏洞排查：零故障、验签失败不得 unlock。
	badInput := dfa.Input{Public: []bool{true}, SignatureValid: false}
	baseTr, err := dfa.Run(prog, badInput)
	must(err)
	fmt.Printf("[1] 无故障/验签失败: unlock=%d emits=%v（预期 unlock=0，emit 走失败事件）\n\n",
		baseTr.UnlockCount, baseTr.Emits)

	// 2) 单点守卫在 K=2 下被双跳击穿的对照。
	single, err := dfa.Splice(prog, []dfa.Candidate{{
		ID: "single-guard", Index: 0, Cost: 10, Gadget: dfa.SigGuard(2),
	}})
	must(err)
	fmt.Println("==== 插入单点 SigGuard 后的程序 ====")
	fmt.Println(single.Disassemble())

	att, err := dfa.Attack(single, badInput, 2)
	must(err)
	fmt.Printf("[2] BFS 攻击: reach=%v 使用故障数=%d 见证=%+v\n",
		att.Reachable, att.FaultsUsed, att.Witness)

	// 显式“双跳”：两次条件反转分别击穿插入守卫与原主校验。
	// 步号必须来自实际执行轨迹（静态地址不等于执行步号，因为 halt 不执行）。
	doubleJump := pureDoubleInvert(single)
	djTr, err := dfa.Replay(single, badInput, doubleJump)
	must(err)
	fmt.Printf("[3] 纯双跳(两次 invert)重放: unlock=%d emits=%v\n\n",
		djTr.UnlockCount, djTr.Emits)

	// 3) 寄存器翻转攻击对照（针对原程序，一次 flip 即可越权）。
	flipAtk, err := dfa.Attack(prog, badInput, 2)
	must(err)
	flipTr, err := dfa.Replay(prog, badInput, flipAtk.Witness)
	must(err)
	fmt.Printf("[4] 寄存器翻转攻击: 见证=%+v 重放 unlock=%d\n\n",
		flipAtk.Witness, flipTr.UnlockCount)

	// 4) 最小代价修补搜索：含 ROM 禁区与多种候选。
	candidates := []dfa.Candidate{
		{ID: "cheap-single", Index: 0, Cost: 10, Gadget: dfa.SigGuard(2)},
		{ID: "rom-blocked", Index: 3, Cost: 1, Gadget: dfa.SigGuard(3)},
		{ID: "triple-head", Index: 0, Cost: 25,
			Gadget: dfa.TripleSigGuard([3]int{2, 3, 4})},
		{ID: "triple-tail", Index: 6, Cost: 40,
			Gadget: dfa.TripleSigGuard([3]int{2, 3, 4})},
	}
	rom := []dfa.ROMRange{{Start: 3, End: 5}} // 原 jt/emit/halt 区域只读
	rep, err := dfa.Analyze(prog, candidates, rom, dfa.DefaultBudgetK)
	must(err)

	fmt.Println("==== ROM 禁区排除 ====")
	for _, ex := range rep.Excluded {
		fmt.Printf("  排除候选 %s: %s\n", ex.Candidate.ID, ex.Reason)
	}
	if rep.FeasibleNotFound || rep.Feasible == nil {
		panic("未找到可行修补方案")
	}
	fmt.Printf("\n[5] 最小代价方案: 候选=%s 总代价=%d 指令数=%d 交叉对照=%v\n",
		rep.Feasible.Candidates[0].ID, rep.Feasible.TotalCost,
		rep.Feasible.InstrCount, rep.Feasible.CrossCheckOK)

	// 5) 有效输入下 unlock 次数与完整 emit 序列一致性。
	good := dfa.Input{Public: []bool{true}, SignatureValid: true}
	origGood, err := dfa.Run(prog, good)
	must(err)
	fixGood, err := dfa.Run(rep.Feasible.Patched, good)
	must(err)
	fmt.Printf("[6] 有效输入一致性: 原 unlock=%d emits=%v | 修补后 unlock=%d emits=%v\n",
		origGood.UnlockCount, origGood.Emits, fixGood.UnlockCount, fixGood.Emits)

	// 6) 独立全枚举预言机对最终方案再做一次复核。
	orc, err := dfa.ExhaustiveOracle(rep.Feasible.Patched, badInput, dfa.DefaultBudgetK)
	must(err)
	fmt.Printf("[7] 全枚举预言机: 可达=%v 枚举轨迹=%d\n",
		orc.Reachable, orc.TrajectoriesRun)

	fmt.Println("\n==== 最终修补程序 ====")
	fmt.Println(rep.Feasible.Patched.Disassemble())
}

// pureDoubleInvert 构造两次条件反转的“双跳”故障。
//
// 无故障且验签失败时，轨迹在插入守卫的第一个 jt 处就 halt，无法直接观察
// 原程序主 jt 的步号；但跳过守卫 halt 后，执行序列连续，因此第二个 jt 的
// 步号 = 第一个 jt 步号 + (两个 jt 的静态下标差) - 1（被跳过的 halt 不占
// 执行步）。该函数针对演示程序的直线布局做了这一显式计算。
func pureDoubleInvert(p *dfa.Program) []dfa.Fault {
	tr, err := dfa.Run(p, dfa.Input{Public: []bool{true}, SignatureValid: false})
	if err != nil {
		panic(err)
	}
	firstStep := -1
	firstAddr := -1
	var jtAddrs []int
	for addr, ins := range p.Instrs {
		if ins.Op == dfa.OpJt {
			jtAddrs = append(jtAddrs, addr)
		}
	}
	if len(jtAddrs) < 2 {
		panic("演示程序至少应包含两个 jt 条件跳转")
	}
	for stepIdx, st := range tr.Steps {
		if st.Op == dfa.OpJt {
			firstStep = stepIdx
			firstAddr = st.PC
			break
		}
	}
	if firstStep < 0 || firstAddr != jtAddrs[0] {
		panic("未能定位插入守卫的第一个 jt")
	}
	staticGap := jtAddrs[1] - jtAddrs[0]
	secondStep := firstStep + staticGap - 1
	return []dfa.Fault{
		{Step: firstStep, Kind: dfa.FaultInvert},
		{Step: secondStep, Kind: dfa.FaultInvert},
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
