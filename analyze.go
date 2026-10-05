package sccp

// Analyzer 保存稀疏条件常量传播（SCCP）的不动点分析结果。
//
// 算法要点：
//  1. 值域是三值格 ⊥ < const < ⊤，合流（Meet）只在格上向上移动，
//     因此循环变量的信息必然单调收敛，在有限步内到达不动点。
//  2. 基本块的入态只合流"已被判定可执行"的前驱边；尚未访问的
//     前驱贡献 ⊥，不参与合流，也不会被误判为永不可达。
//  3. 除零陷阱（div by constant 0）只影响报告阶段的可删除边判定，
//     不影响分析本身的状态传播，从而保证单调性。
type Analyzer struct {
	cfg *CFG

	// Reachable[b]：块 b 是否至少被一条可执行边到达过。
	Reachable []bool
	// Env[b]：块 b 入口处的抽象环境（只合流已可达前驱）。
	Env []map[string]Value
	// ExecEdge[e]：边 e 是否被判定为可执行（只增不删）。
	ExecEdge map[Edge]bool
	// Traps[b]：块 b 是否被证明必然因常量除零而 trap。
	Traps []bool
	// TrapInstr[b]：导致 trap 的指令下标，未 trap 时为 -1。
	TrapInstr []int

	// Updates 记录每一次环境提升（用于检验单调收敛性）。
	Updates []Update
}

// Update 记录一次格值的单调提升：Old < New（严格上升）。
type Update struct {
	Block    int
	Var      string
	Old, New Value
}

// Analyze 对 cfg 运行 SCCP 直到不动点。
func Analyze(cfg *CFG) *Analyzer {
	n := len(cfg.Blocks)
	a := &Analyzer{
		cfg:       cfg,
		Reachable: make([]bool, n),
		Env:       make([]map[string]Value, n),
		ExecEdge:  make(map[Edge]bool),
		Traps:     make([]bool, n),
		TrapInstr: make([]int, n),
	}
	for i := range a.TrapInstr {
		a.TrapInstr[i] = -1
	}

	// 入口块的输入变量视为 ⊤（未知），其余变量为 ⊥。
	entry := cfg.Entry
	a.Env[entry] = make(map[string]Value)
	for _, in := range cfg.Inputs {
		a.Env[entry][in] = Overdefined()
	}
	a.Reachable[entry] = true

	worklist := []int{entry}
	for len(worklist) > 0 {
		id := worklist[0]
		worklist = worklist[1:]
		b := cfg.block(id)

		// 块内传递：从入态出发逐条解释指令。
		st := copyEnv(a.Env[id])
		for i, ins := range b.Instrs {
			res, trap := transfer(ins, st)
			if trap && !a.Traps[id] {
				a.Traps[id] = true
				a.TrapInstr[id] = i
			}
			st[ins.Dst] = res
		}

		// 根据终结符确定当前可执行的出边，并把出态合流进后继。
		for _, succ := range feasibleSuccs(b, st) {
			a.ExecEdge[Edge{id, succ}] = true
			if a.Env[succ] == nil {
				a.Env[succ] = make(map[string]Value)
			}
			changed := false
			if !a.Reachable[succ] {
				a.Reachable[succ] = true
				changed = true
			}
			for k, v := range st {
				old, ok := a.Env[succ][k]
				if !ok {
					old = Undefined()
				}
				nv := Meet(old, v)
				if nv != old {
					// 单调性保证：nv 严格大于 old。
					a.Env[succ][k] = nv
					a.Updates = append(a.Updates, Update{Block: succ, Var: k, Old: old, New: nv})
					changed = true
				}
			}
			if changed {
				worklist = append(worklist, succ)
			}
		}
	}
	return a
}

// copyEnv 复制抽象环境。
func copyEnv(env map[string]Value) map[string]Value {
	out := make(map[string]Value, len(env))
	for k, v := range env {
		out[k] = v
	}
	return out
}

// evalOperand 在抽象环境中求操作数的格值。
func evalOperand(op Operand, st map[string]Value) Value {
	if op.IsConst {
		return Constant(op.Imm)
	}
	if v, ok := st[op.Var]; ok {
		return v
	}
	return Undefined()
}

// transfer 计算单条指令的抽象语义，返回结果格值与是否必然 trap
// （除数为常量 0）。除零时结果保守取 ⊤——该值在具体执行中不可观测，
// 取 ⊤ 保证后续传播依然单调且安全。
func transfer(ins Instr, st map[string]Value) (Value, bool) {
	av := evalOperand(ins.A, st)
	bv := evalOperand(ins.B, st)

	switch ins.Op {
	case OpConst:
		return Constant(ins.B.Imm), false
	case OpCopy:
		return av, false
	}

	// 除数为常量 0：无论被除数是否已知，都必然 trap。
	if ins.Op == OpDiv && bv.Kind == Const && bv.Val == 0 {
		return Overdefined(), true
	}
	if av.Kind == Undef || bv.Kind == Undef {
		return Undefined(), false
	}
	if av.Kind == Over || bv.Kind == Over {
		return Overdefined(), false
	}
	// 两侧均为常量：按 8 位回绕语义折叠。
	return Constant(arith(ins.Op, av.Val, bv.Val)), false
}

// feasibleSuccs 依据当前抽象状态给出块的可执行后继。
// 条件为 ⊥ 时不产生任何出边（尚不知道，而非永不可达）；
// 条件为常量时只产生对应方向；为 ⊤ 时两个方向都可执行。
func feasibleSuccs(b *Block, st map[string]Value) []int {
	switch b.Term.Kind {
	case TermJump:
		return []int{b.Term.Next}
	case TermBranch:
		cv := evalOperand(b.Term.Cond, st)
		switch cv.Kind {
		case Const:
			if cv.Val != 0 {
				return []int{b.Term.True}
			}
			return []int{b.Term.False}
		case Over:
			if b.Term.True == b.Term.False {
				return []int{b.Term.True}
			}
			return []int{b.Term.True, b.Term.False}
		default: // Undef：尚无出边可判定
			return nil
		}
	default: // TermReturn
		return nil
	}
}
