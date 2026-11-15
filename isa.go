package netsec

import "fmt"

const (
	// SecretSize is the fixed secret length. The public boundary is checked
	// before any secret or candidate bytes are read by the VM.
	SecretSize = 32
	// CandidateSize is the fixed candidate length.
	CandidateSize = 32
	// MaxInstructions is the largest program accepted by the checker.
	MaxInstructions = 128
	// NumRegisters is the number of 64-bit VM registers.
	NumRegisters = 16
	// NoRegister can be used in an address operand to mean "use the
	// instruction's static offset only".
	NoRegister uint8 = 0xff
)

// The VM has a small, fixed register file. Registers start at zero on every
// execution and are not visible through the trace.

// Op is a VM instruction opcode. There are exactly six instruction kinds.
type Op uint8

const (
	OpRead   Op = iota + 1 // addressed byte read
	OpXor                  // bitwise exclusive-or
	OpOr                   // bitwise inclusive-or
	OpInc                  // add one
	OpLoop                 // public-count backward loop
	OpBranch               // branch on a register value
)

func (o Op) String() string {
	switch o {
	case OpRead:
		return "read"
	case OpXor:
		return "xor"
	case OpOr:
		return "or"
	case OpInc:
		return "inc"
	case OpLoop:
		return "loop"
	case OpBranch:
		return "branch"
	default:
		return "invalid"
	}
}

// Base identifies the protected memory region addressed by OpRead.
type Base uint8

const (
	BaseSecret    Base = 1
	BaseCandidate Base = 2
)

const (
	// SecretBase is the first absolute address of the 32-byte secret region.
	SecretBase uint32 = 0x00001000
	// CandidateBase is the first absolute address of the 32-byte candidate
	// region.
	CandidateBase uint32 = 0x00002000
)

func (b Base) valid() bool {
	return b == BaseSecret || b == BaseCandidate
}

func (b Base) String() string {
	switch b {
	case BaseSecret:
		return "secret"
	case BaseCandidate:
		return "candidate"
	default:
		return "invalid"
	}
}

// Condition is the value predicate used by OpBranch. Equal and NotEqual
// compare the register with the instruction immediate; Zero and NonZero are
// tests against zero.
type Condition uint8

const (
	CondZero Condition = iota + 1
	CondNonZero
	CondEqual
	CondNotEqual
)

func (c Condition) valid() bool {
	return c >= CondZero && c <= CondNotEqual
}

func (c Condition) String() string {
	switch c {
	case CondZero:
		return "zero"
	case CondNonZero:
		return "nonzero"
	case CondEqual:
		return "equal"
	case CondNotEqual:
		return "not-equal"
	default:
		return "invalid"
	}
}

// Instruction is one VM instruction. The field meaning depends on Op:
//
//	OpRead:   Dst = memory[Base + Offset + regs[Src]] (Src may be NoRegister)
//	OpXor:    Dst = regs[Src] ^ regs[Src2]
//	OpOr:     Dst = regs[Src] | regs[Src2]
//	OpInc:    Dst = regs[Src] + 1
//	OpLoop:   after a body pass, if completed iterations < Count, jump to Target
//	OpBranch: if Condition holds for regs[Src], jump to Target
//
// OpLoop is placed at the end of a loop body. The first encounter follows the
// body's completion of its first pass; Count is a public, fixed upper bound.
// The loop never consults secret or candidate data to decide its count.
type Instruction struct {
	Op Op

	Dst  uint8
	Src  uint8
	Src2 uint8

	Base      Base
	Offset    uint32
	Condition Condition
	Imm       uint64
	Count     uint64
	Target    uint32
}

// Read builds an indexed addressed read. indexReg may be NoRegister.
func Read(dst uint8, base Base, indexReg uint8) Instruction {
	return Instruction{Op: OpRead, Dst: dst, Base: base, Src: indexReg}
}

// ReadOffset builds an addressed read using an index register plus a static
// byte offset.
func ReadOffset(dst uint8, base Base, indexReg uint8, offset uint32) Instruction {
	return Instruction{Op: OpRead, Dst: dst, Base: base, Src: indexReg, Offset: offset}
}

// ReadAt builds an addressed read at a static offset. The index register is
// NoRegister.
func ReadAt(dst uint8, base Base, offset uint32) Instruction {
	return Instruction{Op: OpRead, Dst: dst, Base: base, Src: NoRegister, Offset: offset}
}

// Xor builds dst = a ^ b.
func Xor(dst, a, b uint8) Instruction {
	return Instruction{Op: OpXor, Dst: dst, Src: a, Src2: b}
}

// Or builds dst = a | b.
func Or(dst, a, b uint8) Instruction {
	return Instruction{Op: OpOr, Dst: dst, Src: a, Src2: b}
}

// Inc builds dst = src + 1.
func Inc(dst, src uint8) Instruction {
	return Instruction{Op: OpInc, Dst: dst, Src: src}
}

// Loop builds a public-count loop terminator. bodyStart must precede the loop
// instruction in the program. The body executes exactly count times.
func Loop(count uint64, bodyStart uint32) Instruction {
	return Instruction{Op: OpLoop, Count: count, Target: bodyStart}
}

// Branch builds a value-dependent branch.
func Branch(reg uint8, cond Condition, imm uint64, target uint32) Instruction {
	return Instruction{Op: OpBranch, Src: reg, Condition: cond, Imm: imm, Target: target}
}

// BranchIfZero branches when regs[reg] == 0.
func BranchIfZero(reg uint8, target uint32) Instruction {
	return Branch(reg, CondZero, 0, target)
}

// BranchIfNonZero branches when regs[reg] != 0.
func BranchIfNonZero(reg uint8, target uint32) Instruction {
	return Branch(reg, CondNonZero, 0, target)
}

// BranchIfEqual branches when regs[reg] == imm.
func BranchIfEqual(reg uint8, imm uint64, target uint32) Instruction {
	return Branch(reg, CondEqual, imm, target)
}

// BranchIfNotEqual branches when regs[reg] != imm.
func BranchIfNotEqual(reg uint8, imm uint64, target uint32) Instruction {
	return Branch(reg, CondNotEqual, imm, target)
}

// Program is an immutable-by-convention sequence of at most MaxInstructions
// instructions. Result names the register read as the program's output.
type Program struct {
	Instructions []Instruction
	Result       uint8
}

// Clone returns a program whose instruction slice is independent from p.
func (p Program) Clone() Program {
	out := Program{Result: p.Result}
	if p.Instructions != nil {
		out.Instructions = make([]Instruction, len(p.Instructions))
		copy(out.Instructions, p.Instructions)
	}
	return out
}

// Validate checks the static structure of p. It does not execute it.
func (p Program) Validate() error {
	if len(p.Instructions) > MaxInstructions {
		return fmt.Errorf("%w: %d instructions (maximum %d)", ErrInvalidProgram, len(p.Instructions), MaxInstructions)
	}
	if p.Result >= NumRegisters {
		return fmt.Errorf("%w: result register %d (registers 0..%d)", ErrInvalidProgram, p.Result, NumRegisters-1)
	}
	for pc, ins := range p.Instructions {
		if err := validateInstruction(pc, ins, len(p.Instructions)); err != nil {
			return err
		}
	}
	return nil
}

func validateInstruction(pc int, ins Instruction, programLength int) error {
	invalid := func(format string, args ...any) error {
		return fmt.Errorf("%w: instruction %d (%s): %s", ErrInvalidProgram, pc, ins.Op, fmt.Sprintf(format, args...))
	}
	validReg := func(r uint8) bool { return r < NumRegisters }
	validOptionalReg := func(r uint8) bool { return r == NoRegister || r < NumRegisters }

	switch ins.Op {
	case OpRead:
		if !ins.Base.valid() {
			return invalid("invalid base %d", ins.Base)
		}
		if !validReg(ins.Dst) {
			return invalid("invalid destination register %d", ins.Dst)
		}
		if !validOptionalReg(ins.Src) {
			return invalid("invalid index register %d", ins.Src)
		}
	case OpXor, OpOr:
		if !validReg(ins.Dst) || !validReg(ins.Src) || !validReg(ins.Src2) {
			return invalid("invalid register operand")
		}
	case OpInc:
		if !validReg(ins.Dst) || !validReg(ins.Src) {
			return invalid("invalid register operand")
		}
	case OpLoop:
		if ins.Count == 0 {
			return invalid("loop count must be nonzero")
		}
		if ins.Target >= uint32(pc) {
			return invalid("loop body start %d must precede loop at %d", ins.Target, pc)
		}
		if ins.Target >= uint32(programLength) {
			return invalid("loop target outside program")
		}
	case OpBranch:
		if !validReg(ins.Src) {
			return invalid("invalid condition register %d", ins.Src)
		}
		if !ins.Condition.valid() {
			return invalid("invalid condition %d", ins.Condition)
		}
		if ins.Target > uint32(programLength) {
			return invalid("branch target %d outside program", ins.Target)
		}
	default:
		return invalid("unknown opcode %d", ins.Op)
	}
	return nil
}
