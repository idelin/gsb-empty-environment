package netsec

import "fmt"

// Memory layout of the interpreter. The candidate occupies the low region and
// the secret the high region. Length validation is a public operation and is
// always completed by the host before the secret region is entered.
const (
	CandidateSize = 32
	SecretSize    = 32

	CandidateBase = 0
	SecretBase    = CandidateSize
	MemSize       = SecretBase + SecretSize

	NumRegs = 8

	// MaxProgramLen is the largest program the checker accepts.
	MaxProgramLen = 128
)

// Op is an opcode of the custom instruction set. The set is deliberately
// tiny: addressed reads, XOR, OR, increment, public-bound loops and
// branch-by-value. There are no writes, no indirect calls and no
// secret-dependent memory sizing.
type Op uint8

const (
	// OpRead: R[Dst] = Mem[Imm + R[SrcA]]. Records the read address.
	OpRead Op = iota
	// OpXor: R[Dst] = R[SrcA] ^ R[SrcB].
	OpXor
	// OpOr: R[Dst] = R[SrcA] | R[SrcB].
	OpOr
	// OpInc: R[Dst] = R[SrcA] + 1 (mod 256).
	OpInc
	// OpLoop: execute the Aux instructions following this one Imm times.
	// Imm is a public, static trip count.
	OpLoop
	// OpBranch: if R[SrcA] != 0, jump to pc + Off. Records the outcome.
	OpBranch
)

// Instruction is a single VM instruction. Only the fields documented for
// each Op are meaningful.
type Instruction struct {
	Op   Op
	Dst  uint8
	SrcA uint8
	SrcB uint8
	Imm  uint16 // OpRead: base address. OpLoop: public trip count.
	Aux  uint16 // OpLoop: body length in instructions.
	Off  int16  // OpBranch: relative jump target.
}

// EventKind classifies recorded trace events.
type EventKind uint8

const (
	// EventRead records the address of every memory read.
	EventRead EventKind = iota
	// EventBranch records the outcome of every branch-by-value.
	EventBranch
)

// Event is one observable trace entry.
type Event struct {
	Kind  EventKind
	Addr  uint32 // EventRead: effective address.
	Taken bool   // EventBranch: whether the branch was taken.
}

// Validate statically checks a program: size limit, register indices,
// well-nested structured loops, and branch targets confined to their
// enclosing structural level.
func Validate(prog []Instruction) error {
	if len(prog) == 0 {
		return fmt.Errorf("netsec: empty program")
	}
	if len(prog) > MaxProgramLen {
		return fmt.Errorf("netsec: program too large: %d instructions, limit %d", len(prog), MaxProgramLen)
	}
	return validateRange(prog, 0, len(prog))
}

func validateRange(prog []Instruction, start, end int) error {
	for pc := start; pc < end; {
		ins := prog[pc]
		switch ins.Op {
		case OpRead:
			if ins.Dst >= NumRegs || ins.SrcA >= NumRegs {
				return fmt.Errorf("netsec: pc %d: register out of range", pc)
			}
			pc++
		case OpXor, OpOr:
			if ins.Dst >= NumRegs || ins.SrcA >= NumRegs || ins.SrcB >= NumRegs {
				return fmt.Errorf("netsec: pc %d: register out of range", pc)
			}
			pc++
		case OpInc:
			if ins.Dst >= NumRegs || ins.SrcA >= NumRegs {
				return fmt.Errorf("netsec: pc %d: register out of range", pc)
			}
			pc++
		case OpLoop:
			if ins.Aux == 0 {
				return fmt.Errorf("netsec: pc %d: loop with empty body", pc)
			}
			bodyStart := pc + 1
			bodyEnd := bodyStart + int(ins.Aux)
			if bodyEnd > end {
				return fmt.Errorf("netsec: pc %d: loop body escapes enclosing range", pc)
			}
			if err := validateRange(prog, bodyStart, bodyEnd); err != nil {
				return err
			}
			pc = bodyEnd
		case OpBranch:
			if ins.SrcA >= NumRegs {
				return fmt.Errorf("netsec: pc %d: register out of range", pc)
			}
			target := pc + int(ins.Off)
			if target < start || target > end {
				return fmt.Errorf("netsec: pc %d: branch target %d escapes enclosing range", pc, target)
			}
			pc++
		default:
			return fmt.Errorf("netsec: pc %d: unknown opcode %d", pc, ins.Op)
		}
	}
	return nil
}

// FirstDivergence returns the index of the first event at which traces a and
// b differ, the shorter length if one is a prefix of the other, or -1 when
// the traces are identical.
func FirstDivergence(a, b []Event) int {
	n := min(len(a), len(b))
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	if len(a) != len(b) {
		return n
	}
	return -1
}
