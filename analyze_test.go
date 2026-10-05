package sccp

import (
	"strings"
	"testing"
)

func blk(id int, name string, instrs []Instr, term Terminator) *Block {
	return &Block{ID: id, Name: name, Instrs: instrs, Term: term}
}

func jump(to int) Terminator { return Terminator{Kind: TermJump, Next: to} }
func ret() Terminator        { return Terminator{Kind: TermReturn} }
func br(cond Operand, t, f int) Terminator {
	return Terminator{Kind: TermBranch, Cond: cond, True: t, False: f}
}

func findReplacement(rep *Report, block string, instr int, slot, varName string) (ConstReplacement, bool) {
	for _, r := range rep.Replacements {
		if r.Block == block && r.Instr == instr && r.Slot == slot && r.Var == varName {
			return r, true
		}
	}
	return ConstReplacement{}, false
}

func findDeletion(rep *Report, from, to int) (EdgeDeletion, bool) {
	for _, d := range rep.DeletedEdges {
		if d.FromID == from && d.ToID == to {
			return d, true
		}
	}
	return EdgeDeletion{}, false
}

// 8 位回绕：200 + 100 = 300 mod 256 = 44，分支条件随之折叠；
// 合流点 E 只有一个可达前驱，r 必须保持常量 1 而不是 ⊤。
func TestWrappingAndMergeOnlyReachablePreds(t *testing.T) {
	cfg := &CFG{
		Entry: 0,
		Blocks: []*Block{
			blk(0, "entry", []Instr{
				{Dst: "x", Op: OpConst, B: C(200)},
				{Dst: "y", Op: OpAdd, A: V("x"), B: C(100)},
				{Dst: "c", Op: OpEq, A: V("y"), B: C(44)},
			}, br(V("c"), 1, 2)),
			blk(1, "A", []Instr{{Dst: "r", Op: OpConst, B: C(1)}}, jump(3)),
			blk(2, "B", []Instr{{Dst: "r", Op: OpConst, B: C(2)}}, jump(3)),
			blk(3, "E", nil, ret()),
		},
	}
	a := Analyze(cfg)
	rep := a.Report()

	r, ok := findReplacement(rep, "entry", 1, "A", "x")
	if !ok || r.Value != 200 {
		t.Fatalf("期望 x=200 的替换，得到 %+v, ok=%v", r, ok)
	}
	r, ok = findReplacement(rep, "entry", 2, "A", "y")
	if !ok || r.Value != 44 {
		t.Fatalf("期望 y=44（回绕）的替换，得到 %+v, ok=%v", r, ok)
	}
	d, ok := findDeletion(rep, 0, 2)
	if !ok || !strings.Contains(d.Reason, "常量") {
		t.Fatalf("期望删除 entry->B 且理由含常量说明，得到 %+v, ok=%v", d, ok)
	}
	if got := a.Env[3]["r"]; got != Constant(1) {
		t.Fatalf("合流点 r 应为常量 1（只考虑可达前驱），得到 %v", got)
	}
	if a.Reachable[2] {
		t.Fatalf("块 B 不应可达")
	}
	// B 不可达，其出边只能进入未决列表，不能被删除。
	if _, bad := findDeletion(rep, 2, 3); bad {
		t.Fatalf("不可达块 B 的出边不应被判定删除")
	}
	foundUnresolved := false
	for _, u := range rep.Unresolved {
		if u.FromID == 2 && u.ToID == 3 {
			foundUnresolved = true
		}
	}
	if !foundUnresolved {
		t.Fatalf("B->E 应出现在未决边列表中")
	}
	if v := ValidateExhaustive(cfg, a, rep, 1<<16, 10000); !v.OK() {
		t.Fatalf("穷举对照失败: %v", v.Violations)
	}
}

// 循环：x 在循环头合流 1 与 2，必须单调地 ⊥->const->⊤ 收敛；
// 循环不变量 k 保持常量。
func TestLoopMonotoneConvergence(t *testing.T) {
	cfg := &CFG{
		Entry: 0,
		Blocks: []*Block{
			blk(0, "entry", []Instr{
				{Dst: "x", Op: OpConst, B: C(1)},
				{Dst: "k", Op: OpConst, B: C(7)},
			}, jump(1)),
			blk(1, "head", []Instr{
				{Dst: "y", Op: OpCopy, A: V("x")},
				{Dst: "c", Op: OpLt, A: V("y"), B: C(200)},
			}, br(V("c"), 2, 3)),
			blk(2, "body", []Instr{
				{Dst: "x", Op: OpConst, B: C(2)},
				{Dst: "z", Op: OpCopy, A: V("k")},
			}, jump(1)),
			blk(3, "exit", nil, ret()),
		},
	}
	a := Analyze(cfg)

	if got := a.Env[1]["x"]; got.Kind != Over {
		t.Fatalf("循环头 x 应收敛到 ⊤，得到 %v", got)
	}
	if got := a.Env[1]["k"]; got != Constant(7) {
		t.Fatalf("循环不变量 k 应保持常量 7，得到 %v", got)
	}
	// 每一次环境提升都必须严格沿格上升（单调收敛）。
	for _, u := range a.Updates {
		if !Leq(u.Old, u.New) || u.Old == u.New {
			t.Fatalf("非单调更新: 块 %d 变量 %s: %v -> %v", u.Block, u.Var, u.Old, u.New)
		}
	}
	// 同一 (块, 变量) 的更新链必须首尾衔接、只升不降。
	last := make(map[[2]interface{}]Value)
	for _, u := range a.Updates {
		key := [2]interface{}{u.Block, u.Var}
		if prev, ok := last[key]; ok && prev != u.Old {
			t.Fatalf("更新链断裂: 块 %d 变量 %s: 上次到 %v，本次从 %v 起", u.Block, u.Var, prev, u.Old)
		}
		last[key] = u.New
	}
}

// 常量除零：块被证明必然 trap，其所有出边删除并给出异常理由。
func TestDivByZeroTrap(t *testing.T) {
	cfg := &CFG{
		Entry: 0,
		Blocks: []*Block{
			blk(0, "entry", []Instr{
				{Dst: "d", Op: OpConst, B: C(3)},
				{Dst: "d", Op: OpSub, A: V("d"), B: C(3)},
				{Dst: "q", Op: OpDiv, A: C(10), B: V("d")},
			}, jump(1)),
			blk(1, "next", []Instr{{Dst: "r", Op: OpConst, B: C(1)}}, ret()),
		},
	}
	a := Analyze(cfg)
	rep := a.Report()

	if !a.Traps[0] || a.TrapInstr[0] != 2 {
		t.Fatalf("块 entry 应在指令 2 处被判定必然 trap, Traps=%v TrapInstr=%v", a.Traps, a.TrapInstr)
	}
	d, ok := findDeletion(rep, 0, 1)
	if !ok || !strings.Contains(d.Reason, "除零") {
		t.Fatalf("期望删除 entry->next 且理由含除零说明，得到 %+v, ok=%v", d, ok)
	}
	// 除数应被替换为常量 0。
	r, ok := findReplacement(rep, "entry", 2, "B", "d")
	if !ok || r.Value != 0 {
		t.Fatalf("期望除数 d=0 的替换，得到 %+v, ok=%v", r, ok)
	}
	v := ValidateExhaustive(cfg, a, rep, 1<<16, 10000)
	if !v.OK() {
		t.Fatalf("穷举对照失败: %v", v.Violations)
	}
	if v.ValChecks == 0 {
		t.Fatalf("应当有常量核对发生（trap 前的指令操作数）")
	}
}

// 回绕减法得到 0 再作除数：跨指令的回绕 + 除零联动。
func TestWrapAroundZeroDivisor(t *testing.T) {
	cfg := &CFG{
		Entry: 0,
		Blocks: []*Block{
			blk(0, "entry", []Instr{
				{Dst: "a", Op: OpConst, B: C(5)},
				{Dst: "b", Op: OpConst, B: C(251)},
				{Dst: "d", Op: OpAdd, A: V("a"), B: V("b")}, // 5+251=256 -> 0
				{Dst: "q", Op: OpDiv, A: C(1), B: V("d")},
			}, jump(1)),
			blk(1, "next", nil, ret()),
		},
	}
	a := Analyze(cfg)
	if !a.Traps[0] {
		t.Fatalf("回绕得到 0 的除数应被判定为必然 trap")
	}
	rep := a.Report()
	if _, ok := findDeletion(rep, 0, 1); !ok {
		t.Fatalf("trap 块的出边应被删除")
	}
	if v := ValidateExhaustive(cfg, a, rep, 1<<16, 10000); !v.OK() {
		t.Fatalf("穷举对照失败: %v", v.Violations)
	}
}

// 尚未访问的边不得误判为永不可达：不可达块的出边只能列入未决。
func TestUnvisitedEdgesNotMisjudged(t *testing.T) {
	cfg := &CFG{
		Entry: 0,
		Blocks: []*Block{
			blk(0, "entry", []Instr{{Dst: "x", Op: OpConst, B: C(0)}}, br(V("x"), 1, 2)),
			blk(1, "dead", []Instr{{Dst: "y", Op: OpConst, B: C(1)}}, br(V("y"), 3, 4)),
			blk(2, "live", nil, ret()),
			blk(3, "deadT", nil, ret()),
			blk(4, "deadF", nil, ret()),
		},
	}
	a := Analyze(cfg)
	rep := a.Report()

	if _, ok := findDeletion(rep, 0, 1); !ok {
		t.Fatalf("entry->dead 应被删除（条件为常量 0）")
	}
	for _, d := range rep.DeletedEdges {
		if d.FromID == 1 {
			t.Fatalf("不可达块 dead 的出边 %d->%d 不应被判定删除", d.FromID, d.ToID)
		}
	}
	unresolved := make(map[Edge]bool)
	for _, u := range rep.Unresolved {
		unresolved[Edge{u.FromID, u.ToID}] = true
	}
	if !unresolved[Edge{1, 3}] || !unresolved[Edge{1, 4}] {
		t.Fatalf("dead 的两条出边应都在未决列表中: %+v", rep.Unresolved)
	}
	if v := ValidateExhaustive(cfg, a, rep, 1<<16, 10000); !v.OK() {
		t.Fatalf("穷举对照失败: %v", v.Violations)
	}
}

// 带输入变量的循环程序：对全部 256 种输入做完全穷举对照。
// 循环体依赖 8 位回绕（i 自增回绕后必然等于 n，保证终止）。
func TestExhaustiveWithInputAndLoop(t *testing.T) {
	cfg := &CFG{
		Entry:  0,
		Inputs: []string{"n"},
		Blocks: []*Block{
			blk(0, "entry", []Instr{
				{Dst: "i", Op: OpConst, B: C(0)},
				{Dst: "s", Op: OpConst, B: C(0)},
				{Dst: "w", Op: OpConst, B: C(200)},
				{Dst: "w", Op: OpAdd, A: V("w"), B: C(100)}, // 回绕为 44
			}, jump(1)),
			blk(1, "head", []Instr{
				{Dst: "c", Op: OpEq, A: V("i"), B: V("n")},
			}, br(V("c"), 3, 2)),
			blk(2, "body", []Instr{
				{Dst: "s", Op: OpAdd, A: V("s"), B: V("w")},
				{Dst: "i", Op: OpAdd, A: V("i"), B: C(1)},
			}, jump(1)),
			blk(3, "exit", []Instr{
				{Dst: "m", Op: OpCopy, A: V("n")},
				{Dst: "z", Op: OpConst, B: C(0)},
				{Dst: "has", Op: OpNe, A: V("m"), B: C(0)},
			}, br(V("has"), 4, 5)),
			// 仅当 n != 0 时可达：除以 n 永远安全。
			blk(4, "div", []Instr{
				{Dst: "q", Op: OpDiv, A: C(100), B: V("m")},
			}, ret()),
			blk(5, "nodiv", nil, ret()),
		},
	}
	a := Analyze(cfg)
	rep := a.Report()

	// w 回绕为 44，循环体内应可替换。
	if r, ok := findReplacement(rep, "body", 0, "B", "w"); !ok || r.Value != 44 {
		t.Fatalf("期望循环体内 w=44 的替换，得到 %+v, ok=%v", r, ok)
	}
	// 除数 m 不是常量（等于输入 n），不得产生 trap 判定。
	if a.Traps[4] {
		t.Fatalf("除数为 ⊤ 时不应判定 trap")
	}
	// 完全穷举 256 种输入。
	v := ValidateExhaustive(cfg, a, rep, 1<<16, 1<<20)
	if !v.OK() {
		t.Fatalf("穷举对照失败: %v", v.Violations)
	}
	if v.Runs != 256 {
		t.Fatalf("应完全穷举 256 种输入，实际 %d", v.Runs)
	}
}

// 两个方向都可到达但赋同一常量：合流后仍是常量。
func TestMergeSameConstant(t *testing.T) {
	cfg := &CFG{
		Entry:  0,
		Inputs: []string{"a"},
		Blocks: []*Block{
			blk(0, "entry", []Instr{{Dst: "c", Op: OpEq, A: V("a"), B: C(0)}}, br(V("c"), 1, 2)),
			blk(1, "T", []Instr{{Dst: "x", Op: OpConst, B: C(5)}}, jump(3)),
			blk(2, "F", []Instr{{Dst: "x", Op: OpConst, B: C(5)}}, jump(3)),
			blk(3, "J", []Instr{{Dst: "y", Op: OpCopy, A: V("x")}}, ret()),
		},
	}
	a := Analyze(cfg)
	if got := a.Env[3]["x"]; got != Constant(5) {
		t.Fatalf("两路同常量合流应仍为常量 5，得到 %v", got)
	}
	rep := a.Report()
	if v := ValidateExhaustive(cfg, a, rep, 1<<16, 10000); !v.OK() {
		t.Fatalf("穷举对照失败: %v", v.Violations)
	}
}
