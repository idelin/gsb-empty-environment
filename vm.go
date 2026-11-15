package netsec

import (
	"errors"
	"fmt"
)

var (
	// ErrInvalidLength is returned before any protected memory is read when a
	// secret or candidate is not exactly 32 bytes.
	ErrInvalidLength = errors.New("secret and candidate must both be exactly 32 bytes")
	// ErrInvalidProgram reports a statically invalid or oversized program.
	ErrInvalidProgram = errors.New("invalid program")
	// ErrAddress reports an addressed read outside the fixed protected
	// regions.
	ErrAddress = errors.New("address outside secret/candidate region")
	// ErrUnknownBudget reports that no execution budget was supplied. An
	// unknown budget never means that trace equality has been established.
	ErrUnknownBudget = errors.New("unknown execution budget")
	// ErrBudgetExceeded reports that the program did not finish within the
	// supplied number of instruction steps. The resulting execution is
	// incomplete and must not be treated as evidence of constant behavior.
	ErrBudgetExceeded = errors.New("execution budget exceeded")
)

// RunConfig supplies the public, finite instruction-step budget. MaxSteps must
// be greater than zero. There is no fallback or "unlimited" default.
type RunConfig struct {
	MaxSteps uint64
}

// Validate reports whether the budget is known and usable.
func (c RunConfig) Validate() error {
	if c.MaxSteps == 0 {
		return ErrUnknownBudget
	}
	return nil
}

// Execution is the result of one VM run. Output is regs[Program.Result].
// Registers and Trace are returned for diagnostics and checking; the trace
// contains only addressed reads and branch outcomes.
type Execution struct {
	Output    uint64
	Registers [NumRegisters]uint64
	Trace     Trace
	Steps     uint64
	Halted    bool
}

// Equal reports the built-in comparison convention: a zero result register
// means equal. It is meaningful only for programs that use that convention.
func (e Execution) Equal() bool {
	return e.Halted && e.Output == 0
}

// Execute validates slice lengths before entering the protected memory
// regions, then executes program over the two 32-byte inputs.
func Execute(program Program, secret, candidate []byte, cfg RunConfig) (Execution, error) {
	if len(secret) != SecretSize || len(candidate) != CandidateSize {
		return Execution{}, ErrInvalidLength
	}
	var s, c [SecretSize]byte
	copy(s[:], secret)
	copy(c[:], candidate)
	return Execute32(program, s, c, cfg)
}

// Execute32 executes program over fixed-size inputs, which inherently
// satisfies the length boundary before execution.
func Execute32(program Program, secret, candidate [SecretSize]byte, cfg RunConfig) (Execution, error) {
	if err := cfg.Validate(); err != nil {
		return Execution{}, err
	}
	if err := program.Validate(); err != nil {
		return Execution{}, err
	}

	var regs [NumRegisters]uint64
	var trace Trace
	loopIterations := make(map[uint32]uint64)

	var pc uint32
	var steps uint64
	for pc < uint32(len(program.Instructions)) {
		if steps >= cfg.MaxSteps {
			return Execution{
				Registers: regs,
				Trace:     trace,
				Steps:     steps,
			}, fmt.Errorf("%w after %d steps at pc %d", ErrBudgetExceeded, steps, pc)
		}
		steps++

		instructionPC := pc
		ins := program.Instructions[pc]
		switch ins.Op {
		case OpRead:
			value, address, err := readProtected(ins, &regs, secret, candidate)
			if err != nil {
				return Execution{
					Registers: regs,
					Trace:     trace,
					Steps:     steps,
				}, fmt.Errorf("%w: instruction %d: %v", ErrAddress, pc, err)
			}
			regs[ins.Dst] = uint64(value)
			trace = append(trace, Event{
				Kind:    EventRead,
				PC:      pc,
				Address: address,
			})
			pc++

		case OpXor:
			regs[ins.Dst] = regs[ins.Src] ^ regs[ins.Src2]
			pc++

		case OpOr:
			regs[ins.Dst] = regs[ins.Src] | regs[ins.Src2]
			pc++

		case OpInc:
			regs[ins.Dst] = regs[ins.Src] + 1
			pc++

		case OpLoop:
			completed := loopIterations[pc] + 1
			taken := completed < ins.Count
			if taken {
				loopIterations[pc] = completed
				pc = ins.Target
			} else {
				delete(loopIterations, pc)
				pc++
			}
			trace = append(trace, Event{
				Kind:  EventBranch,
				PC:    instructionPC,
				Taken: taken,
			})

		case OpBranch:
			taken := conditionHolds(ins.Condition, regs[ins.Src], ins.Imm)
			if taken {
				pc = ins.Target
			} else {
				pc++
			}
			trace = append(trace, Event{
				Kind:  EventBranch,
				PC:    instructionPC,
				Taken: taken,
			})

		default:
			return Execution{
				Registers: regs,
				Trace:     trace,
				Steps:     steps,
			}, fmt.Errorf("%w: unknown opcode %d at pc %d", ErrInvalidProgram, ins.Op, pc)
		}
	}

	result := regs[program.Result]
	return Execution{
		Output:    result,
		Registers: regs,
		Trace:     trace,
		Steps:     steps,
		Halted:    true,
	}, nil
}

// Run is a budget-by-value convenience wrapper around Execute.
func Run(program Program, secret, candidate []byte, maxSteps uint64) (Execution, error) {
	return Execute(program, secret, candidate, RunConfig{MaxSteps: maxSteps})
}

// Run32 is a budget-by-value convenience wrapper around Execute32.
func Run32(program Program, secret, candidate [SecretSize]byte, maxSteps uint64) (Execution, error) {
	return Execute32(program, secret, candidate, RunConfig{MaxSteps: maxSteps})
}

func readProtected(ins Instruction, regs *[NumRegisters]uint64, secret, candidate [SecretSize]byte) (byte, uint32, error) {
	index := uint64(0)
	if ins.Src != NoRegister {
		index = regs[ins.Src]
	}
	offset := uint64(ins.Offset)
	if index > uint64(^uint32(0)) || offset > uint64(^uint32(0))-index {
		return 0, 0, fmt.Errorf("%w: index overflow", ErrAddress)
	}
	relative := offset + index
	if relative >= SecretSize {
		return 0, 0, fmt.Errorf("%w: relative byte %d", ErrAddress, relative)
	}

	switch ins.Base {
	case BaseSecret:
		return secret[relative], SecretBase + uint32(relative), nil
	case BaseCandidate:
		return candidate[relative], CandidateBase + uint32(relative), nil
	default:
		return 0, 0, fmt.Errorf("%w: unknown base %d", ErrAddress, ins.Base)
	}
}

func conditionHolds(cond Condition, value, imm uint64) bool {
	switch cond {
	case CondZero:
		return value == 0
	case CondNonZero:
		return value != 0
	case CondEqual:
		return value == imm
	case CondNotEqual:
		return value != imm
	default:
		return false
	}
}
