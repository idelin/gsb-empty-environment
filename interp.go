package sccp

// Outcome is the observable result of running a function: either it
// returns a value, it traps (division by zero), or it diverges
// (exceeded the step cap).
type Outcome struct {
	Trap     bool
	Diverged bool
	Ret      uint8
}

// Run interprets f concretely with the given 8-bit inputs.
// stepCap bounds the number of block executions; exceeding it reports
// divergence so that equivalence checking can compare non-terminating
// behaviour as well.
func Run(f *Func, inputs []uint8, stepCap int) Outcome {
	env := map[string]uint8{}
	b := f.Entry
	for steps := 0; ; steps++ {
		if steps > stepCap {
			return Outcome{Diverged: true}
		}
		blk := f.Blocks[b]
		for _, in := range blk.Insts {
			switch in.Op {
			case OpInput:
				env[in.Dst] = inputs[in.InputIdx]
			case OpConst:
				env[in.Dst] = in.Imm
			default:
				v, trap := evalConcrete(in.Op, env[in.A], env[in.B])
				if trap {
					return Outcome{Trap: true}
				}
				env[in.Dst] = v
			}
		}
		t := blk.Term
		switch t.Kind {
		case TermJump:
			b = t.Next
		case TermBr:
			if env[t.Cond] != 0 {
				b = t.ThenBlk
			} else {
				b = t.ElseBlk
			}
		case TermRet:
			return Outcome{Ret: env[t.RetVar]}
		case TermTrap:
			return Outcome{Trap: true}
		}
	}
}
