package fwaudit

import "fmt"

// OOB describes an out-of-bounds array read observed during Run.
type OOB struct {
	Instr int   // instruction index of the faulting load
	Array int   // array that was indexed
	Index uint8 // out-of-bounds index value
}

// Run executes p concretely on input. Execution stops at the first
// out-of-bounds load (the firmware faults) and reports it; otherwise it
// returns nil. Reading an input position beyond len(input) is an error.
func Run(p *Program, input []uint8) (*OOB, error) {
	regs := make([]uint8, p.NRegs)
	pc := p.Entry
	for pc != Halt {
		ins := p.Instrs[pc]
		switch ins.Op {
		case OpInput:
			if ins.Pos >= len(input) {
				return nil, fmt.Errorf("instr %d: input byte %d not provided", pc, ins.Pos)
			}
			regs[ins.Dst] = input[ins.Pos]
			pc = ins.Next[0]
		case OpAnd:
			regs[ins.Dst] &= ins.Imm
			pc = ins.Next[0]
		case OpAdd:
			regs[ins.Dst] += ins.Imm
			pc = ins.Next[0]
		case OpJnz:
			if regs[ins.Src] != 0 {
				pc = ins.Next[1]
			} else {
				pc = ins.Next[0]
			}
		case OpLoad:
			idx := regs[ins.Src]
			arr := p.Arrays[ins.Arr]
			if int(idx) >= len(arr) {
				return &OOB{Instr: pc, Array: ins.Arr, Index: idx}, nil
			}
			regs[ins.Dst] = arr[idx]
			pc = ins.Next[0]
		default:
			return nil, fmt.Errorf("instr %d: unknown op %d", pc, int(ins.Op))
		}
	}
	return nil, nil
}
