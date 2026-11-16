package netsec

import (
	"errors"
	"fmt"
)

// ErrStepBudgetExceeded is returned when execution consumes more steps than
// the caller-supplied budget allows.
var ErrStepBudgetExceeded = errors.New("netsec: step budget exceeded")

// VM interprets programs over the fixed 64-byte memory and records an
// observable trace: every read address and every branch outcome. It never
// measures wall-clock time.
type VM struct {
	Mem      [MemSize]byte
	R        [NumRegs]byte
	Trace    []Event
	MaxSteps uint64

	steps uint64
}

// Run validates prog and executes it from a clean register file. The memory
// image is left as the caller prepared it. Trace is reset on entry.
func (vm *VM) Run(prog []Instruction) error {
	if err := Validate(prog); err != nil {
		return err
	}
	vm.R = [NumRegs]byte{}
	vm.Trace = vm.Trace[:0]
	vm.steps = 0
	return vm.exec(prog, 0, len(prog))
}

// Steps reports how many instructions the last Run executed.
func (vm *VM) Steps() uint64 { return vm.steps }

func (vm *VM) step() error {
	vm.steps++
	if vm.steps > vm.MaxSteps {
		return ErrStepBudgetExceeded
	}
	return nil
}

func (vm *VM) exec(prog []Instruction, start, end int) error {
	for pc := start; pc < end; {
		ins := prog[pc]
		if err := vm.step(); err != nil {
			return err
		}
		switch ins.Op {
		case OpRead:
			addr := uint32(ins.Imm) + uint32(vm.R[ins.SrcA])
			if addr >= MemSize {
				return fmt.Errorf("netsec: pc %d: read address %d out of range", pc, addr)
			}
			vm.R[ins.Dst] = vm.Mem[addr]
			vm.Trace = append(vm.Trace, Event{Kind: EventRead, Addr: addr})
			pc++
		case OpXor:
			vm.R[ins.Dst] = vm.R[ins.SrcA] ^ vm.R[ins.SrcB]
			pc++
		case OpOr:
			vm.R[ins.Dst] = vm.R[ins.SrcA] | vm.R[ins.SrcB]
			pc++
		case OpInc:
			vm.R[ins.Dst] = vm.R[ins.SrcA] + 1
			pc++
		case OpLoop:
			bodyStart := pc + 1
			bodyEnd := bodyStart + int(ins.Aux)
			for i := uint16(0); i < ins.Imm; i++ {
				if err := vm.exec(prog, bodyStart, bodyEnd); err != nil {
					return err
				}
			}
			pc = bodyEnd
		case OpBranch:
			taken := vm.R[ins.SrcA] != 0
			vm.Trace = append(vm.Trace, Event{Kind: EventBranch, Taken: taken})
			if taken {
				pc += int(ins.Off)
			} else {
				pc++
			}
		default:
			return fmt.Errorf("netsec: pc %d: unknown opcode %d", pc, ins.Op)
		}
	}
	return nil
}
