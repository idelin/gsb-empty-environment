package netsec

import (
	"errors"
	"fmt"
	"math/bits"
)

// Validate checks every structural bound of a program: instruction count,
// variable widths, register indices, branch targets and the presence of a
// terminating return. A validated program is guaranteed to make bounded,
// forward progress.
func Validate(p *Program) error {
	if p == nil {
		return errors.New("validate: nil program")
	}
	if len(p.Code) == 0 {
		return errors.New("validate: empty program")
	}
	if len(p.Code) > MaxInstr {
		return fmt.Errorf("validate: %d instructions exceed MaxInstr %d", len(p.Code), MaxInstr)
	}
	if len(p.Vars) == 0 || len(p.Vars) > 256 {
		return fmt.Errorf("validate: variable count %d out of range", len(p.Vars))
	}
	if int(p.Entry) >= len(p.Code) {
		return fmt.Errorf("validate: entry pc %d out of range", p.Entry)
	}
	names := make(map[string]bool, len(p.Vars))
	for i, v := range p.Vars {
		if v.Name == "" {
			return fmt.Errorf("validate: variable %d has no name", i)
		}
		if names[v.Name] {
			return fmt.Errorf("validate: duplicate variable name %q", v.Name)
		}
		names[v.Name] = true
		if v.Bits < 1 || v.Bits > 64 {
			return fmt.Errorf("validate: variable %q width %d out of [1,64]", v.Name, v.Bits)
		}
		if v.Init > mask(v.Bits) {
			return fmt.Errorf("validate: initial value of %q exceeds %d bits", v.Name, v.Bits)
		}
	}

	hasReturn := false
	for pc, ins := range p.Code {
		if ins.Op >= opCount {
			return fmt.Errorf("validate: pc %d unknown opcode %d", pc, ins.Op)
		}
		switch ins.Op {
		case OpConst:
			if err := checkDst(p, pc, ins.Dst); err != nil {
				return err
			}
			if ins.Immediate > mask(p.Vars[ins.Dst].Bits) {
				return fmt.Errorf("validate: pc %d immediate exceeds destination width", pc)
			}
		case OpLoad, OpAssert, OpReturn:
			if err := checkSrc(p, pc, ins.Src); err != nil {
				return err
			}
			if ins.Op == OpLoad {
				if err := checkDst(p, pc, ins.Dst); err != nil {
					return err
				}
			}
			if ins.Op == OpReturn {
				hasReturn = true
			}
		case OpAdd, OpSub, OpMul, OpDiv, OpCmp:
			if err := checkDst(p, pc, ins.Dst); err != nil {
				return err
			}
			if err := checkSrc(p, pc, ins.Src); err != nil {
				return err
			}
			if err := checkSrc(p, pc, ins.Src2); err != nil {
				return err
			}
		case OpBranch:
			if err := checkSrc(p, pc, ins.Src); err != nil {
				return err
			}
			// Only forward or same-block targets keep execution bounded
			// without an external step counter.
			if int(ins.Target) <= pc || int(ins.Target) >= len(p.Code) {
				return fmt.Errorf("validate: pc %d branch target %d must be strictly forward", pc, ins.Target)
			}
		case OpHost:
			if err := checkDst(p, pc, ins.Dst); err != nil {
				return err
			}
			if err := checkSrc(p, pc, ins.Src); err != nil {
				return err
			}
		}
	}
	if !hasReturn {
		return errors.New("validate: program contains no OpReturn")
	}
	return nil
}

func checkDst(p *Program, pc int, idx uint8) error {
	if int(idx) >= len(p.Vars) {
		return fmt.Errorf("validate: pc %d destination %d out of range", pc, idx)
	}
	return nil
}

func checkSrc(p *Program, pc int, idx uint8) error {
	if int(idx) >= len(p.Vars) {
		return fmt.Errorf("validate: pc %d source %d out of range", pc, idx)
	}
	return nil
}

func mask(bits uint8) uint64 {
	if bits == 64 {
		return ^uint64(0)
	}
	return 1<<bits - 1
}

// Run executes a program with the supplied public input. Public variables not
// present in input keep their declared initial values. host may be nil when
// the program contains no OpHost instruction.
func Run(p *Program, input Input, host Host) (*Trace, error) {
	if err := Validate(p); err != nil {
		return nil, err
	}
	regs, err := initialRegisters(p, input)
	if err != nil {
		return nil, err
	}
	if err := requireHost(p, host); err != nil {
		return nil, err
	}
	return execute(p, regs, host), nil
}

func initialRegisters(p *Program, input Input) ([]uint64, error) {
	regs := make([]uint64, len(p.Vars))
	for i, v := range p.Vars {
		regs[i] = v.Init
	}
	for idx, val := range input {
		if int(idx) >= len(p.Vars) {
			return nil, fmt.Errorf("run: input variable %d out of range", idx)
		}
		if p.Vars[idx].Visibility == Secret {
			return nil, fmt.Errorf("run: variable %d is secret and cannot be set via public input", idx)
		}
		if val > mask(p.Vars[idx].Bits) {
			return nil, fmt.Errorf("run: input value for variable %d exceeds its width", idx)
		}
		regs[idx] = val
	}
	return regs, nil
}

func requireHost(p *Program, host Host) error {
	for _, ins := range p.Code {
		if ins.Op == OpHost && host == nil {
			return errors.New("run: program calls a host but no Host was provided")
		}
	}
	return nil
}

// execute is the single stepping core shared by Run, Replay and the verifier.
func execute(p *Program, regs []uint64, host Host) *Trace {
	trace := &Trace{
		Initial: append([]uint64(nil), regs...),
		Steps:   make([]Step, 0, len(p.Code)),
		ExcPC:   uint8(len(p.Code)),
	}
	pc := int(p.Entry)

	for pc >= 0 && pc < len(p.Code) {
		ins := p.Code[pc]
		snapshot := append([]uint64(nil), regs...)
		step := Step{State: State{PC: uint8(pc), Vars: snapshot}, Instr: ins}

		next, exc := stepInstr(p, ins, regs, host, &step, trace)
		trace.Steps = append(trace.Steps, step)
		if exc != ExcNone {
			trace.Exc = exc
			trace.ExcPC = uint8(pc)
			return trace
		}
		if next < 0 {
			// OpReturn terminates cleanly; its return value was already set.
			return trace
		}
		pc = next
	}

	// Fell off the end without OpReturn.
	trace.Exc = ExcNoReturn
	trace.ExcPC = uint8(len(p.Code))
	return trace
}

// stepInstr applies one instruction to regs and returns the next pc. A
// non-zero ExcCode terminates the run immediately.
func stepInstr(p *Program, ins Instr, regs []uint64, host Host, step *Step, trace *Trace) (int, ExcCode) {
	pc := int(step.State.PC)
	next := pc + 1
	switch ins.Op {
	case OpConst:
		regs[ins.Dst] = ins.Immediate & mask(p.Vars[ins.Dst].Bits)

	case OpLoad:
		regs[ins.Dst] = regs[ins.Src]

	case OpAdd:
		a, b := regs[ins.Src], regs[ins.Src2]
		sum, carry := bits.Add64(a, b, 0)
		if carry != 0 {
			return next, ExcOverflow
		}
		regs[ins.Dst] = sum & mask(p.Vars[ins.Dst].Bits)

	case OpSub:
		a, b := regs[ins.Src], regs[ins.Src2]
		diff, borrow := bits.Sub64(a, b, 0)
		if borrow != 0 {
			return next, ExcUnderflow
		}
		regs[ins.Dst] = diff & mask(p.Vars[ins.Dst].Bits)

	case OpMul:
		hi, lo := bits.Mul64(regs[ins.Src], regs[ins.Src2])
		if hi != 0 {
			return next, ExcOverflow
		}
		regs[ins.Dst] = lo & mask(p.Vars[ins.Dst].Bits)

	case OpDiv:
		divisor := regs[ins.Src2]
		if divisor == 0 {
			return next, ExcDivByZero
		}
		regs[ins.Dst] = (regs[ins.Src] / divisor) & mask(p.Vars[ins.Dst].Bits)

	case OpCmp:
		if regs[ins.Src] < regs[ins.Src2] {
			regs[ins.Dst] = 1
		} else {
			regs[ins.Dst] = 0
		}

	case OpAssert:
		if regs[ins.Src] == 0 {
			return next, ExcAssertion
		}

	case OpBranch:
		if regs[ins.Src] != 0 {
			next = int(ins.Target)
		}

	case OpHost:
		result, fault := host.Call(ins.HostID, regs[ins.Src])
		step.Call = &Call{
			HostID: ins.HostID,
			Arg:    regs[ins.Src],
			Result: result & mask(p.Vars[ins.Dst].Bits),
			Fault:  fault,
		}
		if fault {
			return next, ExcHost
		}
		regs[ins.Dst] = step.Call.Result

	case OpReturn:
		trace.Return = regs[ins.Src]
		return -1, ExcNone
	}
	return next, ExcNone
}
