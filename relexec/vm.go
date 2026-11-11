package relexec

import (
	"errors"
	"fmt"
)

// RegCount is the number of 8-bit general purpose registers (r0..r7).
const RegCount = 8

// MaxArrayLen is the maximum supported byte-array length.
const MaxArrayLen = 16

// MaxInputLen is the maximum number of input bytes; input byte i is loaded
// into register ri before execution starts.
const MaxInputLen = 8

// Op identifies an instruction opcode.
type Op uint8

const (
	OpHalt   Op = iota // stop execution; no operands
	OpConst            // r[Dst] = Imm
	OpMov              // r[Dst] = r[Src1]
	OpNot              // r[Dst] = ^r[Src1]
	OpNeg              // r[Dst] = -r[Src1] (mod 256)
	OpAdd              // r[Dst] = r[Src1] + r[Src2] (mod 256)
	OpSub              // r[Dst] = r[Src1] - r[Src2] (mod 256)
	OpMul              // r[Dst] = r[Src1] * r[Src2] (mod 256)
	OpAnd              // r[Dst] = r[Src1] & r[Src2]
	OpOr               // r[Dst] = r[Src1] | r[Src2]
	OpXor              // r[Dst] = r[Src1] ^ r[Src2]
	OpShl              // r[Dst] = r[Src1] << (r[Src2] & 7)
	OpShr              // r[Dst] = r[Src1] >> (r[Src2] & 7)
	OpCmpEq            // r[Dst] = 1 if r[Src1] == r[Src2], else 0
	OpCmpLt            // r[Dst] = 1 if int8(r[Src1]) < int8(r[Src2]), else 0
	OpCmpLtu           // r[Dst] = 1 if r[Src1] < r[Src2] (unsigned), else 0
	OpJmp              // pc = Target
	OpJnz              // if r[Src1] != 0 { pc = Target }
	OpJz               // if r[Src1] == 0 { pc = Target }
	OpLoad             // r[Dst] = array[r[Src1]], traps out of bounds
	OpStore            // array[r[Src1]] = r[Src2], traps out of bounds
)

// String returns the mnemonic of the opcode.
func (op Op) String() string {
	switch op {
	case OpHalt:
		return "HALT"
	case OpConst:
		return "CONST"
	case OpMov:
		return "MOV"
	case OpNot:
		return "NOT"
	case OpNeg:
		return "NEG"
	case OpAdd:
		return "ADD"
	case OpSub:
		return "SUB"
	case OpMul:
		return "MUL"
	case OpAnd:
		return "AND"
	case OpOr:
		return "OR"
	case OpXor:
		return "XOR"
	case OpShl:
		return "SHL"
	case OpShr:
		return "SHR"
	case OpCmpEq:
		return "CMPEQ"
	case OpCmpLt:
		return "CMPLT"
	case OpCmpLtu:
		return "CMPLTU"
	case OpJmp:
		return "JMP"
	case OpJnz:
		return "JNZ"
	case OpJz:
		return "JZ"
	case OpLoad:
		return "LOAD"
	case OpStore:
		return "STORE"
	default:
		return fmt.Sprintf("OP(%d)", uint8(op))
	}
}

// Instr is a single machine instruction. Only the fields relevant to the
// opcode are consulted; see the Op constants for operand conventions.
type Instr struct {
	Op     Op
	Dst    uint8 // destination register
	Src1   uint8 // first source register / index register for Load and Store
	Src2   uint8 // second source register / value register for Store
	Imm    uint8 // immediate for OpConst
	Target int   // absolute program-counter target for jump opcodes
}

// Program is a loop-free instruction sequence. Loop-freedom is enforced
// statically: every jump target must be a strictly higher program counter, so
// no execution can revisit an instruction.
type Program struct {
	Code []Instr
}

// Write records one successful array store in execution order.
type Write struct {
	Index uint8
	Value uint8
}

// Outcome is the observable result of executing one program on one input.
type Outcome struct {
	// Halted reports whether an OpHalt instruction was reached. A false value
	// means execution ran past the last instruction, which is also a normal
	// completion.
	Halted bool
	// HaltPC is the program counter of OpHalt when Halted is true.
	HaltPC int
	// OOB reports that execution trapped because an array access used an
	// index outside [0, ArrayLen).
	OOB bool
	// OOBPC is the program counter of the trapping Load or Store.
	OOBPC int
	// OOBIndex is the index used by the trapping access.
	OOBIndex uint8
	// OOBStore distinguishes a trapping Store (true) from a trapping Load.
	OOBStore bool
	// Return is the value of r0 at completion. It is zero on OOB traps.
	Return uint8
	// Writes is the ordered trace of successful stores.
	Writes []Write
	// Steps is the number of instructions executed, including the one that
	// trapped or halted.
	Steps int64
}

// ErrBudgetExhausted is returned by Run when the instruction-step budget is
// consumed before the program completes.
var ErrBudgetExhausted = errors.New("relexec: step budget exhausted")

// Validate checks the program structure: known opcodes, valid register
// operands and strictly forward in-range jump targets.
func (p *Program) Validate() error {
	if p == nil {
		return errors.New("relexec: nil program")
	}
	for pc, ins := range p.Code {
		reg := func(r uint8, name string) error {
			if r >= RegCount {
				return fmt.Errorf("relexec: %s at pc %d uses invalid register r%d (%s)", ins.Op, pc, r, name)
			}
			return nil
		}
		switch ins.Op {
		case OpHalt:
		case OpConst:
			if err := reg(ins.Dst, "Dst"); err != nil {
				return err
			}
		case OpMov, OpNot, OpNeg:
			if err := reg(ins.Dst, "Dst"); err != nil {
				return err
			}
			if err := reg(ins.Src1, "Src1"); err != nil {
				return err
			}
		case OpAdd, OpSub, OpMul, OpAnd, OpOr, OpXor, OpShl, OpShr,
			OpCmpEq, OpCmpLt, OpCmpLtu:
			if err := reg(ins.Dst, "Dst"); err != nil {
				return err
			}
			if err := reg(ins.Src1, "Src1"); err != nil {
				return err
			}
			if err := reg(ins.Src2, "Src2"); err != nil {
				return err
			}
		case OpJmp:
			if ins.Target <= pc || ins.Target >= len(p.Code) {
				return fmt.Errorf("relexec: %s at pc %d targets non-forward pc %d", ins.Op, pc, ins.Target)
			}
		case OpJnz, OpJz:
			if err := reg(ins.Src1, "Src1"); err != nil {
				return err
			}
			if ins.Target <= pc || ins.Target >= len(p.Code) {
				return fmt.Errorf("relexec: %s at pc %d targets non-forward pc %d", ins.Op, pc, ins.Target)
			}
		case OpLoad:
			if err := reg(ins.Dst, "Dst"); err != nil {
				return err
			}
			if err := reg(ins.Src1, "Src1"); err != nil {
				return err
			}
		case OpStore:
			if err := reg(ins.Src1, "Src1"); err != nil {
				return err
			}
			if err := reg(ins.Src2, "Src2"); err != nil {
				return err
			}
		default:
			return fmt.Errorf("relexec: unknown opcode %d at pc %d", uint8(ins.Op), pc)
		}
	}
	return nil
}

// Run executes the program on the given input with a zero-initialized array of
// arrayLen bytes. Every executed instruction, including Halt and a trapping
// access, costs one step. It returns ErrBudgetExhausted when budget runs out
// before completion; an out-of-bounds access itself is reported on the
// Outcome, not as an error. UnlimitedBudget removes the step cap.
func Run(p *Program, input []byte, arrayLen int, budget int64) (*Outcome, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if arrayLen < 1 || arrayLen > MaxArrayLen {
		return nil, fmt.Errorf("relexec: array length %d out of range [1,%d]", arrayLen, MaxArrayLen)
	}
	if len(input) > MaxInputLen {
		return nil, fmt.Errorf("relexec: input length %d exceeds %d", len(input), MaxInputLen)
	}
	if budget < UnlimitedBudget {
		return nil, errors.New("relexec: negative budget")
	}

	var regs [RegCount]uint8
	copy(regs[:], input)
	mem := make([]uint8, arrayLen)
	writes := make([]Write, 0)

	out := &Outcome{}
	pc := 0
	for pc < len(p.Code) {
		if budget != UnlimitedBudget && out.Steps >= budget {
			return nil, ErrBudgetExhausted
		}
		ins := p.Code[pc]
		out.Steps++

		switch ins.Op {
		case OpHalt:
			out.Halted = true
			out.HaltPC = pc
			pc = len(p.Code)
			continue
		case OpConst:
			regs[ins.Dst] = ins.Imm
		case OpMov:
			regs[ins.Dst] = regs[ins.Src1]
		case OpNot:
			regs[ins.Dst] = ^regs[ins.Src1]
		case OpNeg:
			regs[ins.Dst] = -regs[ins.Src1]
		case OpAdd:
			regs[ins.Dst] = regs[ins.Src1] + regs[ins.Src2]
		case OpSub:
			regs[ins.Dst] = regs[ins.Src1] - regs[ins.Src2]
		case OpMul:
			regs[ins.Dst] = regs[ins.Src1] * regs[ins.Src2]
		case OpAnd:
			regs[ins.Dst] = regs[ins.Src1] & regs[ins.Src2]
		case OpOr:
			regs[ins.Dst] = regs[ins.Src1] | regs[ins.Src2]
		case OpXor:
			regs[ins.Dst] = regs[ins.Src1] ^ regs[ins.Src2]
		case OpShl:
			regs[ins.Dst] = regs[ins.Src1] << (regs[ins.Src2] & 7)
		case OpShr:
			regs[ins.Dst] = regs[ins.Src1] >> (regs[ins.Src2] & 7)
		case OpCmpEq:
			if regs[ins.Src1] == regs[ins.Src2] {
				regs[ins.Dst] = 1
			} else {
				regs[ins.Dst] = 0
			}
		case OpCmpLt:
			if int8(regs[ins.Src1]) < int8(regs[ins.Src2]) {
				regs[ins.Dst] = 1
			} else {
				regs[ins.Dst] = 0
			}
		case OpCmpLtu:
			if regs[ins.Src1] < regs[ins.Src2] {
				regs[ins.Dst] = 1
			} else {
				regs[ins.Dst] = 0
			}
		case OpJmp:
			pc = ins.Target
			continue
		case OpJnz:
			if regs[ins.Src1] != 0 {
				pc = ins.Target
				continue
			}
		case OpJz:
			if regs[ins.Src1] == 0 {
				pc = ins.Target
				continue
			}
		case OpLoad:
			idx := regs[ins.Src1]
			if int(idx) >= len(mem) {
				out.OOB = true
				out.OOBPC = pc
				out.OOBIndex = idx
				out.Writes = writes
				return out, nil
			}
			regs[ins.Dst] = mem[idx]
		case OpStore:
			idx := regs[ins.Src1]
			if int(idx) >= len(mem) {
				out.OOB = true
				out.OOBPC = pc
				out.OOBIndex = idx
				out.OOBStore = true
				out.Writes = writes
				return out, nil
			}
			mem[idx] = regs[ins.Src2]
			writes = append(writes, Write{Index: idx, Value: regs[ins.Src2]})
		}
		pc++
	}

	out.Return = regs[0]
	out.Writes = writes
	return out, nil
}
