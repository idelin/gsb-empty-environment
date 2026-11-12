// Package unwind evaluates untrusted stack-based unwind expressions
// recovered from crash dumps. Expressions compute stack frame locations
// for the host unwinder, but they come from an untrusted source, so
// evaluation is sandboxed:
//
//   - The instruction set is limited to constants, register reads,
//     add/subtract, dereference, and conditional jumps.
//   - Memory is read exclusively through a host-authorized Reader
//     callback; the evaluator never touches host memory directly.
//   - Evaluation is metered twice: by executed instruction steps and
//     by bytes read through the Reader.
//   - Register inputs are copied into a fixed snapshot before
//     evaluation starts, so later mutation of the caller's slice
//     cannot influence the result.
//
// On any failure Eval returns a zero result together with a non-nil
// error. Callers must discard the result when err != nil; the package
// never fabricates a complete-looking result for a failed expression.
package unwind

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// Op is an unwind expression opcode.
type Op uint8

const (
	// OpConst pushes Imm onto the stack.
	OpConst Op = iota
	// OpReg pushes the value of register Imm from the fixed snapshot.
	OpReg
	// OpAdd pops b then a and pushes a+b (wrapping uint64 arithmetic).
	OpAdd
	// OpSub pops b then a and pushes a-b (wrapping uint64 arithmetic).
	OpSub
	// OpDeref pops an address and pushes the DerefSize-byte little-endian
	// value read from it through the authorized Reader.
	OpDeref
	// OpJmpIf pops a condition; if non-zero, execution continues at
	// instruction index Imm, otherwise at the next instruction.
	OpJmpIf
)

// DerefSize is the number of bytes read by a single OpDeref.
const DerefSize = 8

// Instr is a single decoded instruction. Imm holds the literal for
// OpConst, the register index for OpReg, and the absolute target
// instruction index for OpJmpIf.
type Instr struct {
	Op  Op
	Imm uint64
}

// Reader is the host-authorized memory window. Implementations decide
// which addresses the unwinder may observe. ReadAt must fill buf
// completely, returning len(buf), or report failure via a short count
// and/or a non-nil error; the evaluator treats anything less than a
// full read as fatal.
type Reader interface {
	ReadAt(addr uint64, buf []byte) (int, error)
}

// Limits bounds a single evaluation. A zero field allows none of that
// resource, so the zero Limits denies all work.
type Limits struct {
	// MaxSteps caps the number of executed instructions.
	MaxSteps uint64
	// MaxReadBytes caps the total bytes read through the Reader.
	MaxReadBytes uint64
	// MaxStack caps the operand stack depth; <= 0 selects a default.
	MaxStack int
}

// DefaultLimits returns conservative limits suitable for ordinary
// frame-location expressions.
func DefaultLimits() Limits {
	return Limits{MaxSteps: 256, MaxReadBytes: 4096, MaxStack: 64}
}

// Stats reports the resources consumed by an evaluation. It is
// returned on success and failure alike so the host can bill partial
// work, but it never implies a usable result.
type Stats struct {
	Steps     uint64
	ReadBytes uint64
}

var (
	ErrStepLimit   = errors.New("unwind: instruction step limit exceeded")
	ErrReadLimit   = errors.New("unwind: read byte budget exceeded")
	ErrShortRead   = errors.New("unwind: memory reader returned a short read")
	ErrAddrWrap    = errors.New("unwind: dereference address range wraps around")
	ErrUnderflow   = errors.New("unwind: operand stack underflow")
	ErrStackLimit  = errors.New("unwind: operand stack depth limit exceeded")
	ErrNoResult    = errors.New("unwind: expression left no result")
	ErrExtraResult = errors.New("unwind: expression left more than one result")
	ErrBadProgram  = errors.New("unwind: invalid program")
	ErrBadRegister = errors.New("unwind: register index out of range")
	ErrNilReader   = errors.New("unwind: nil memory reader")
)

// Decode parses an untrusted expression blob into a program.
//
// Encoding: one opcode byte, then operands — 8-byte little-endian for
// OpConst, 1 byte register index for OpReg, 2-byte little-endian
// instruction index for OpJmpIf; the remaining ops have no operand.
func Decode(blob []byte) ([]Instr, error) {
	var prog []Instr
	for i := 0; i < len(blob); {
		op := Op(blob[i])
		i++
		switch op {
		case OpConst:
			if len(blob)-i < 8 {
				return nil, fmt.Errorf("%w: truncated const operand", ErrBadProgram)
			}
			prog = append(prog, Instr{Op: op, Imm: binary.LittleEndian.Uint64(blob[i:])})
			i += 8
		case OpReg:
			if len(blob)-i < 1 {
				return nil, fmt.Errorf("%w: truncated reg operand", ErrBadProgram)
			}
			prog = append(prog, Instr{Op: op, Imm: uint64(blob[i])})
			i++
		case OpJmpIf:
			if len(blob)-i < 2 {
				return nil, fmt.Errorf("%w: truncated jmpif operand", ErrBadProgram)
			}
			prog = append(prog, Instr{Op: op, Imm: uint64(binary.LittleEndian.Uint16(blob[i:]))})
			i += 2
		case OpAdd, OpSub, OpDeref:
			prog = append(prog, Instr{Op: op})
		default:
			return nil, fmt.Errorf("%w: unknown opcode 0x%02x", ErrBadProgram, byte(op))
		}
	}
	if err := validate(prog); err != nil {
		return nil, err
	}
	return prog, nil
}

func validate(prog []Instr) error {
	if len(prog) == 0 {
		return fmt.Errorf("%w: empty program", ErrBadProgram)
	}
	for pc, ins := range prog {
		switch ins.Op {
		case OpConst, OpReg, OpAdd, OpSub, OpDeref:
		case OpJmpIf:
			if ins.Imm >= uint64(len(prog)) {
				return fmt.Errorf("%w: jump target %d out of range at pc %d", ErrBadProgram, ins.Imm, pc)
			}
		default:
			return fmt.Errorf("%w: unknown opcode 0x%02x at pc %d", ErrBadProgram, byte(ins.Op), pc)
		}
	}
	return nil
}

// Eval runs prog against the register snapshot and authorized reader
// and returns the single value left on the stack. Any failure yields a
// zero result and a non-nil error; partial results are never returned.
func Eval(prog []Instr, regs []uint64, r Reader, lim Limits) (uint64, Stats, error) {
	var st Stats
	if err := validate(prog); err != nil {
		return 0, st, err
	}
	if r == nil {
		return 0, st, ErrNilReader
	}
	maxStack := lim.MaxStack
	if maxStack <= 0 {
		maxStack = 64
	}

	// Fixed snapshot: the caller's register slice is copied once and
	// never consulted again during evaluation.
	snapshot := make([]uint64, len(regs))
	copy(snapshot, regs)

	stack := make([]uint64, 0, 8)
	push := func(v uint64) error {
		if len(stack) >= maxStack {
			return ErrStackLimit
		}
		stack = append(stack, v)
		return nil
	}
	pop := func() (uint64, error) {
		if len(stack) == 0 {
			return 0, ErrUnderflow
		}
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		return v, nil
	}

	pc := 0
	for pc < len(prog) {
		if st.Steps >= lim.MaxSteps {
			return 0, st, ErrStepLimit
		}
		st.Steps++
		ins := prog[pc]
		switch ins.Op {
		case OpConst:
			if err := push(ins.Imm); err != nil {
				return 0, st, err
			}
			pc++
		case OpReg:
			if ins.Imm >= uint64(len(snapshot)) {
				return 0, st, fmt.Errorf("%w: r%d of %d", ErrBadRegister, ins.Imm, len(snapshot))
			}
			if err := push(snapshot[ins.Imm]); err != nil {
				return 0, st, err
			}
			pc++
		case OpAdd, OpSub:
			b, err := pop()
			if err != nil {
				return 0, st, err
			}
			a, err := pop()
			if err != nil {
				return 0, st, err
			}
			if ins.Op == OpAdd {
				err = push(a + b)
			} else {
				err = push(a - b)
			}
			if err != nil {
				return 0, st, err
			}
			pc++
		case OpDeref:
			addr, err := pop()
			if err != nil {
				return 0, st, err
			}
			if addr > math.MaxUint64-(DerefSize-1) {
				return 0, st, fmt.Errorf("%w: %#x + %d", ErrAddrWrap, addr, DerefSize)
			}
			if st.ReadBytes+DerefSize > lim.MaxReadBytes {
				return 0, st, ErrReadLimit
			}
			var buf [DerefSize]byte
			n, rerr := r.ReadAt(addr, buf[:])
			if rerr != nil {
				return 0, st, fmt.Errorf("unwind: read %#x: %w", addr, rerr)
			}
			if n != DerefSize {
				return 0, st, fmt.Errorf("%w: got %d of %d bytes at %#x", ErrShortRead, n, DerefSize, addr)
			}
			st.ReadBytes += DerefSize
			if err := push(binary.LittleEndian.Uint64(buf[:])); err != nil {
				return 0, st, err
			}
			pc++
		case OpJmpIf:
			cond, err := pop()
			if err != nil {
				return 0, st, err
			}
			if cond != 0 {
				pc = int(ins.Imm) // target validated against program length
			} else {
				pc++
			}
		default:
			return 0, st, fmt.Errorf("%w: unknown opcode 0x%02x at pc %d", ErrBadProgram, byte(ins.Op), pc)
		}
	}

	if len(stack) == 0 {
		return 0, st, ErrNoResult
	}
	if len(stack) > 1 {
		return 0, st, ErrExtraResult
	}
	return stack[0], st, nil
}
