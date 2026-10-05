// Package sccp implements sparse conditional constant propagation
// (Wegman-Zadeck style) over control-flow graphs of 8-bit machines.
//
// All arithmetic is performed on uint8 and wraps around modulo 256.
// Division and modulo by zero raise a trap (exception) in the concrete
// semantics, which the analysis treats as "this block never completes".
package sccp

import (
	"fmt"
	"strings"
)

// Op is the operation of an instruction.
type Op int

const (
	OpInput Op = iota // Dst = input[InputIdx]
	OpConst           // Dst = Imm
	OpCopy            // Dst = A
	OpAdd             // Dst = A + B  (mod 256)
	OpSub             // Dst = A - B  (mod 256)
	OpMul             // Dst = A * B  (mod 256)
	OpAnd             // Dst = A & B
	OpOr              // Dst = A | B
	OpXor             // Dst = A ^ B
	OpDiv             // Dst = A / B  (traps if B == 0)
	OpMod             // Dst = A % B  (traps if B == 0)
	OpEq              // Dst = (A == B) ? 1 : 0
	OpNe              // Dst = (A != B) ? 1 : 0
	OpLt              // Dst = (A <  B) ? 1 : 0
)

var opNames = map[Op]string{
	OpInput: "input", OpConst: "const", OpCopy: "copy",
	OpAdd: "add", OpSub: "sub", OpMul: "mul",
	OpAnd: "and", OpOr: "or", OpXor: "xor",
	OpDiv: "div", OpMod: "mod",
	OpEq: "eq", OpNe: "ne", OpLt: "lt",
}

func (o Op) String() string { return opNames[o] }

// Inst is a single assignment Dst = Op(A, B).
// OpInput uses InputIdx; OpConst uses Imm; OpCopy uses only A.
type Inst struct {
	Dst      string
	Op       Op
	A, B     string
	Imm      uint8
	InputIdx int
}

func (i Inst) String() string {
	switch i.Op {
	case OpInput:
		return fmt.Sprintf("%s = input #%d", i.Dst, i.InputIdx)
	case OpConst:
		return fmt.Sprintf("%s = const %d", i.Dst, i.Imm)
	case OpCopy:
		return fmt.Sprintf("%s = copy %s", i.Dst, i.A)
	default:
		return fmt.Sprintf("%s = %s %s, %s", i.Dst, i.Op, i.A, i.B)
	}
}

// TermKind classifies block terminators.
type TermKind int

const (
	TermJump TermKind = iota // unconditional jump to Next
	TermBr                   // if Cond != 0 goto ThenBlk else goto ElseBlk
	TermRet                  // return RetVar
	TermTrap                 // unconditional trap (only produced by the optimizer)
)

// Term is a block terminator.
type Term struct {
	Kind    TermKind
	Cond    string
	ThenBlk int
	ElseBlk int
	Next    int
	RetVar  string
}

func (t Term) String() string {
	switch t.Kind {
	case TermJump:
		return fmt.Sprintf("jmp b%d", t.Next)
	case TermBr:
		return fmt.Sprintf("br %s, b%d, b%d", t.Cond, t.ThenBlk, t.ElseBlk)
	case TermRet:
		return fmt.Sprintf("ret %s", t.RetVar)
	case TermTrap:
		return "trap"
	}
	return "?"
}

// Block is a basic block: straight-line instructions plus a terminator.
type Block struct {
	ID    int
	Insts []Inst
	Term  Term
}

func (b *Block) String() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "b%d:\n", b.ID)
	for _, in := range b.Insts {
		fmt.Fprintf(&sb, "  %s\n", in)
	}
	fmt.Fprintf(&sb, "  %s\n", b.Term)
	return sb.String()
}

// Func is a whole control-flow graph.
// Invariant: Blocks[i].ID == i, and every terminator target is a valid index.
type Func struct {
	Name      string
	NumInputs int
	Entry     int
	Blocks    []*Block
}

func (f *Func) String() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "func %s(inputs=%d) entry=b%d\n", f.Name, f.NumInputs, f.Entry)
	for _, b := range f.Blocks {
		sb.WriteString(b.String())
	}
	return sb.String()
}

// evalConcrete executes one arithmetic instruction concretely.
// It returns the 8-bit wrapped result, or trap=true for division by zero.
func evalConcrete(op Op, a, b uint8) (v uint8, trap bool) {
	switch op {
	case OpCopy:
		return a, false
	case OpAdd:
		return a + b, false
	case OpSub:
		return a - b, false
	case OpMul:
		return a * b, false
	case OpAnd:
		return a & b, false
	case OpOr:
		return a | b, false
	case OpXor:
		return a ^ b, false
	case OpDiv:
		if b == 0 {
			return 0, true
		}
		return a / b, false
	case OpMod:
		if b == 0 {
			return 0, true
		}
		return a % b, false
	case OpEq:
		if a == b {
			return 1, false
		}
		return 0, false
	case OpNe:
		if a != b {
			return 1, false
		}
		return 0, false
	case OpLt:
		if a < b {
			return 1, false
		}
		return 0, false
	}
	return 0, false
}
