package netsec

// Outcome describes one bounded execution.
type Outcome struct {
	Halted          bool // halted (or ran off the end) within MaxSteps
	Steps           int  // executed instruction slots, including a skipped one
	Unlocks         int  // unlock events observed
	FirstUnlockStep int  // step index of the first unlock, -1 if none
}

// Run executes code with the given public input and host-given
// verification result. If skip >= 0, the single non-halt instruction at
// that index is skipped (a skipped jump simply falls through to the next
// instruction). Registers start all false; control flow past the last
// instruction halts. Execution stops after MaxSteps steps, in which case
// Halted is false and the run cannot be reported safe.
func Run(code []Instr, input uint, ver bool, skip int) Outcome {
	var regs [NumRegs]bool
	out := Outcome{FirstUnlockStep: -1}
	pc := 0
	for out.Steps = 0; out.Steps < MaxSteps; out.Steps++ {
		if pc >= len(code) {
			out.Halted = true
			return out
		}
		if pc == skip {
			pc++
			continue
		}
		switch ins := code[pc]; ins.Op {
		case OpReadPub:
			regs[ins.A] = (input>>uint(ins.B))&1 == 1
			pc++
		case OpReadVer:
			regs[ins.A] = ver
			pc++
		case OpAnd:
			regs[ins.A] = regs[ins.A] && regs[ins.B]
			pc++
		case OpNot:
			regs[ins.A] = !regs[ins.A]
			pc++
		case OpCJmp:
			if regs[ins.A] {
				pc = ins.B
			} else {
				pc++
			}
		case OpUnlock:
			out.Unlocks++
			if out.FirstUnlockStep < 0 {
				out.FirstUnlockStep = out.Steps
			}
			pc++
		case OpHalt:
			out.Halted = true
			out.Steps++
			return out
		case OpGuard:
			if !ver {
				out.Halted = true
				out.Steps++
				return out
			}
			pc++
		}
	}
	return out
}
