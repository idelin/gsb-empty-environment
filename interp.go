package sccp

// Outcome 是一次具体执行的结局。
type Outcome int

const (
	OutReturn    Outcome = iota // 正常 return
	OutTrap                     // 除零异常
	OutStepLimit                // 超过步数上限（可能不终止）
)

// RunResult 记录一次具体执行的结果。
type RunResult struct {
	Outcome   Outcome
	Steps     int
	TrapBlock int // Outcome == OutTrap 时有效
	TrapInstr int
}

// OperandHook 在具体求值每个操作数时回调（block、指令下标、槽位、
// 操作数、具体值）。instr 为 -1 表示终结符的分支条件。
type OperandHook func(blockID, instr int, slot string, op Operand, concrete uint8)

// EdgeHook 在具体经过一条控制流边时回调。
type EdgeHook func(e Edge)

// Run 以给定输入具体执行 cfg，最多执行 stepBound 步。
// 未定义的变量默认为 0（确定性的具体语义）。
func Run(cfg *CFG, inputs map[string]uint8, stepBound int, opHook OperandHook, edgeHook EdgeHook) RunResult {
	env := make(map[string]uint8, len(inputs))
	for k, v := range inputs {
		env[k] = v
	}
	eval := func(op Operand) uint8 {
		if op.IsConst {
			return op.Imm
		}
		return env[op.Var]
	}

	cur := cfg.Entry
	var res RunResult
	res.TrapBlock, res.TrapInstr = -1, -1
	for res.Steps = 0; res.Steps < stepBound; res.Steps++ {
		b := cfg.block(cur)
		for i, ins := range b.Instrs {
			var av, bv uint8
			switch ins.Op {
			case OpConst:
				bv = eval(ins.B)
				if opHook != nil {
					opHook(cur, i, "B", ins.B, bv)
				}
				env[ins.Dst] = ins.B.Imm
				continue
			case OpCopy:
				av = eval(ins.A)
				if opHook != nil {
					opHook(cur, i, "A", ins.A, av)
				}
				env[ins.Dst] = av
				continue
			default:
				av = eval(ins.A)
				bv = eval(ins.B)
				if opHook != nil {
					opHook(cur, i, "A", ins.A, av)
					opHook(cur, i, "B", ins.B, bv)
				}
			}
			if ins.Op == OpDiv && bv == 0 {
				res.Outcome = OutTrap
				res.TrapBlock, res.TrapInstr = cur, i
				return res
			}
			env[ins.Dst] = arith(ins.Op, av, bv)
		}

		switch b.Term.Kind {
		case TermJump:
			if edgeHook != nil {
				edgeHook(Edge{cur, b.Term.Next})
			}
			cur = b.Term.Next
		case TermBranch:
			cv := eval(b.Term.Cond)
			if opHook != nil {
				opHook(cur, -1, "Cond", b.Term.Cond, cv)
			}
			next := b.Term.False
			if cv != 0 {
				next = b.Term.True
			}
			if edgeHook != nil {
				edgeHook(Edge{cur, next})
			}
			cur = next
		default: // TermReturn
			res.Outcome = OutReturn
			return res
		}
	}
	res.Outcome = OutStepLimit
	return res
}
