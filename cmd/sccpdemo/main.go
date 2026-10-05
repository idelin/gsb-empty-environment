// sccpdemo 演示控制流图上的稀疏条件常量传播（SCCP）：
// 输出常量替换、删边及理由，并用穷举执行逐一对照。
package main

import (
	"fmt"

	"sccp"
)

func main() {
	// 示例程序（8 位回绕语义）：
	//   entry: x=200; y=x+100(=44 回绕); c=(y==44); if c goto loop_init else dead
	//   loop_init: i=0; s=0
	//   head: c2=(i<n); if c2 goto body else exit
	//   body: s=s+i; i=i+1; goto head
	//   exit: z=10; d=z-10(=0); q=100/d(除零 trap!); goto after
	//   after: return
	//   dead: w=1; if w goto deadT else deadF   （从入口不可达）
	cfg := &sccp.CFG{
		Entry:  0,
		Inputs: []string{"n"},
		Blocks: []*sccp.Block{
			{ID: 0, Name: "entry", Instrs: []sccp.Instr{
				{Dst: "x", Op: sccp.OpConst, B: sccp.C(200)},
				{Dst: "y", Op: sccp.OpAdd, A: sccp.V("x"), B: sccp.C(100)},
				{Dst: "c", Op: sccp.OpEq, A: sccp.V("y"), B: sccp.C(44)},
			}, Term: sccp.Terminator{Kind: sccp.TermBranch, Cond: sccp.V("c"), True: 1, False: 5}},
			{ID: 1, Name: "loop_init", Instrs: []sccp.Instr{
				{Dst: "i", Op: sccp.OpConst, B: sccp.C(0)},
				{Dst: "s", Op: sccp.OpConst, B: sccp.C(0)},
			}, Term: sccp.Terminator{Kind: sccp.TermJump, Next: 2}},
			{ID: 2, Name: "head", Instrs: []sccp.Instr{
				{Dst: "c2", Op: sccp.OpLt, A: sccp.V("i"), B: sccp.V("n")},
			}, Term: sccp.Terminator{Kind: sccp.TermBranch, Cond: sccp.V("c2"), True: 3, False: 4}},
			{ID: 3, Name: "body", Instrs: []sccp.Instr{
				{Dst: "s", Op: sccp.OpAdd, A: sccp.V("s"), B: sccp.V("i")},
				{Dst: "i", Op: sccp.OpAdd, A: sccp.V("i"), B: sccp.C(1)},
			}, Term: sccp.Terminator{Kind: sccp.TermJump, Next: 2}},
			{ID: 4, Name: "exit", Instrs: []sccp.Instr{
				{Dst: "z", Op: sccp.OpConst, B: sccp.C(10)},
				{Dst: "d", Op: sccp.OpSub, A: sccp.V("z"), B: sccp.C(10)},
				{Dst: "q", Op: sccp.OpDiv, A: sccp.C(100), B: sccp.V("d")},
			}, Term: sccp.Terminator{Kind: sccp.TermJump, Next: 8}},
			{ID: 5, Name: "dead", Instrs: []sccp.Instr{
				{Dst: "w", Op: sccp.OpConst, B: sccp.C(1)},
			}, Term: sccp.Terminator{Kind: sccp.TermBranch, Cond: sccp.V("w"), True: 6, False: 7}},
			{ID: 6, Name: "deadT", Term: sccp.Terminator{Kind: sccp.TermReturn}},
			{ID: 7, Name: "deadF", Term: sccp.Terminator{Kind: sccp.TermReturn}},
			{ID: 8, Name: "after", Term: sccp.Terminator{Kind: sccp.TermReturn}},
		},
	}

	a := sccp.Analyze(cfg)
	rep := a.Report()

	fmt.Println("=== 常量替换 ===")
	for _, r := range rep.Replacements {
		where := fmt.Sprintf("指令 %d 槽位 %s", r.Instr, r.Slot)
		if r.Instr < 0 {
			where = "终结符分支条件"
		}
		fmt.Printf("  块 %-9s %s：%s -> %d\n", r.Block, where, r.Var, r.Value)
	}

	fmt.Println("\n=== 删除边（附理由） ===")
	for _, d := range rep.DeletedEdges {
		fmt.Printf("  %s -> %s：%s\n", d.From, d.To, d.Reason)
	}

	fmt.Println("\n=== 未决边（源块不可达，不误判为永不可达） ===")
	for _, u := range rep.Unresolved {
		fmt.Printf("  %s -> %s：%s\n", u.From, u.To, u.Reason)
	}

	fmt.Println("\n=== 循环/合流后的抽象环境 ===")
	for _, b := range cfg.Blocks {
		if !a.Reachable[b.ID] {
			fmt.Printf("  块 %-9s 不可达\n", b.Name)
			continue
		}
		fmt.Printf("  块 %-9s 入态: %v", b.Name, a.Env[b.ID])
		if a.Traps[b.ID] {
			fmt.Printf("  [必然除零 trap @ 指令 %d]", a.TrapInstr[b.ID])
		}
		fmt.Println()
	}

	v := sccp.ValidateExhaustive(cfg, a, rep, 1<<16, 1<<20)
	fmt.Printf("\n=== 穷举执行对照 ===\n")
	fmt.Printf("  输入向量: %d 个，边检查: %d 次，值核对: %d 次\n", v.Runs, v.EdgeChecks, v.ValChecks)
	if v.OK() {
		fmt.Println("  结果: 全部通过 —— 删边与常量替换均与具体执行一致")
	} else {
		for _, s := range v.Violations {
			fmt.Println("  反例:", s)
		}
	}
}
