package packetvm

import "fmt"

// Opcode enumerates the data-plane instruction set.
//
// The machine has two 32-bit registers, R0 (working) and R1 (scratch for
// the right-hand side of register/register comparisons), and a forward-only
// control flow graph. Every instruction is fixed size so jump offsets are
// plain instruction counts.
type Opcode uint8

const (
	// OpImm loads an immediate constant into R0.
	OpImm Opcode = iota
	// OpLen loads the runtime packet length (in bytes) into R0.
	OpLen
	// OpLoad reads Size bytes from the packet at a fixed Offset into R0.
	OpLoad
	// OpJmp is an unconditional forward jump.
	OpJmp
	// OpJmpIf jumps forward when the unsigned comparison R0 <op> Arg holds,
	// where Arg is either an immediate or R1.
	OpJmpIf
	// OpRet ends execution and reports a boolean verdict.
	OpRet
)

func (op Opcode) String() string {
	switch op {
	case OpImm:
		return "IMM"
	case OpLen:
		return "LEN"
	case OpLoad:
		return "LOAD"
	case OpJmp:
		return "JMP"
	case OpJmpIf:
		return "JMPIF"
	case OpRet:
		return "RET"
	default:
		return fmt.Sprintf("op(%d)", uint8(op))
	}
}

// MaxInstructions bounds programs accepted from tenants.
const MaxInstructions = 4096

// Reg identifies one of the two machine registers.
type Reg uint8

const (
	// R0 is the working register holding comparison left-hand sides.
	R0 Reg = iota
	// R1 is the scratch register holding comparison right-hand sides.
	R1
)

// r0/r1 are unexported shorthands used during codegen.
const (
	r0 = R0
	r1 = R1
)

// Insn is one compiled instruction.
//
// Meaning by opcode:
//
//	OpImm:   Imm holds the constant.
//	OpLen:   no operands.
//	OpLoad:  Offset and Size describe the fixed-length read.
//	OpJmp:   Off is the (positive) forward jump distance.
//	OpJmpIf: Off is the positive forward distance when Cond matches
//	R0 vs Imm (UseReg=false) or R0 vs R1 (UseReg=true).
//	OpRet:   Verdict is the boolean result.
type Insn struct {
	Op      Opcode
	Imm     uint32
	Offset  uint32
	Size    Size
	Cond    CmpOp
	Off     uint32
	Verdict bool
	// Dst is the destination register for Imm, Len and Load.
	Dst Reg
	// UseReg selects the JmpIf comparison mode: false compares R0 against
	// Imm; true compares R0 against the scratch register R1.
	UseReg bool

	// Origin identifies the source expression that produced this
	// instruction; the verifier uses it to attribute rejection reasons.
	Origin Source
}

// Program is a verified, importable sequence of instructions.
type Program struct {
	RuleName string
	Insns    []Insn
}

// String renders a human-readable disassembly, useful for tenant diagnostics.
func (p *Program) String() string {
	var b []byte
	for i, in := range p.Insns {
		b = append(b, fmt.Sprintf("%-4d %s", i, in.String())...)
		b = append(b, '\n')
	}
	return string(b)
}

func (in Insn) String() string {
	switch in.Op {
	case OpImm:
		return fmt.Sprintf("IMM     %s, #%d", regName(in.Dst), in.Imm)
	case OpLen:
		return fmt.Sprintf("LEN     %s", regName(in.Dst))
	case OpLoad:
		return fmt.Sprintf("LOAD    %s, off=%d size=%d", regName(in.Dst), in.Offset, in.Size)
	case OpJmp:
		return fmt.Sprintf("JMP     +%d", in.Off)
	case OpJmpIf:
		if in.UseReg {
			return fmt.Sprintf("JMPIF   r0 %s r1 +%d", in.Cond, in.Off)
		}
		return fmt.Sprintf("JMPIF   r0 %s #%d +%d", in.Cond, in.Imm, in.Off)
	case OpRet:
		if in.Verdict {
			return "RET     true"
		}
		return "RET     false"
	default:
		return fmt.Sprintf("op(%d)", uint8(in.Op))
	}
}

func regName(r Reg) string {
	if r == R1 {
		return "r1"
	}
	return "r0"
}
