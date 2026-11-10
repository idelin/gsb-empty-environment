// Package fwaudit audits firewall-firmware bytecode for reachable
// out-of-bounds array reads and computes, for each vulnerable site, the
// lexicographically smallest input that replays the fault.
package fwaudit

import "fmt"

// Op is a bytecode opcode. Programs form an acyclic instruction graph:
// every instruction names its successor(s), and -1 means halt.
type Op int

const (
	OpInput Op = iota // Reg[Dst] = Input[Pos]
	OpAnd             // Reg[Dst] &= Imm
	OpAdd             // Reg[Dst] += Imm (mod 256)
	OpJnz             // if Reg[Src] != 0 goto Next[1] else goto Next[0]
	OpLoad            // Reg[Dst] = Arrays[Arr][Reg[Src]]
)

func (o Op) String() string {
	switch o {
	case OpInput:
		return "input"
	case OpAnd:
		return "and"
	case OpAdd:
		return "add"
	case OpJnz:
		return "jnz"
	case OpLoad:
		return "load"
	}
	return fmt.Sprintf("op(%d)", int(o))
}

// MaxRegs bounds the register file. Registers are 8-bit.
const MaxRegs = 16

// Halt is the successor index that terminates execution.
const Halt = -1

type Instr struct {
	Op   Op
	Dst  int    // destination register (OpInput, OpAnd, OpAdd, OpLoad)
	Src  int    // source register (OpJnz condition, OpLoad index)
	Imm  uint8  // constant for OpAnd / OpAdd
	Pos  int    // input byte position for OpInput
	Arr  int    // array id for OpLoad
	Next [2]int // successors; OpJnz uses both, others use Next[0]
}

type Program struct {
	NRegs  int
	Entry  int
	Instrs []Instr
	Arrays [][]uint8
}

// nexts returns the successor indices of ins.
func nexts(ins Instr) []int {
	if ins.Op == OpJnz {
		return []int{ins.Next[0], ins.Next[1]}
	}
	return []int{ins.Next[0]}
}

// Validate checks structural well-formedness, including acyclicity.
func (p *Program) Validate() error {
	if p.NRegs < 1 || p.NRegs > MaxRegs {
		return fmt.Errorf("nregs %d out of range [1,%d]", p.NRegs, MaxRegs)
	}
	if len(p.Instrs) == 0 {
		if p.Entry != Halt {
			return fmt.Errorf("empty program must have entry -1")
		}
		return nil
	}
	if p.Entry < 0 || p.Entry >= len(p.Instrs) {
		return fmt.Errorf("entry %d out of range", p.Entry)
	}
	for i, a := range p.Arrays {
		if len(a) == 0 || len(a) > 255 {
			return fmt.Errorf("array %d: length %d out of range [1,255]", i, len(a))
		}
	}
	checkReg := func(r int, what string, i int) error {
		if r < 0 || r >= p.NRegs {
			return fmt.Errorf("instr %d: %s register r%d out of range", i, what, r)
		}
		return nil
	}
	checkNext := func(n, i int) error {
		if n != Halt && (n < 0 || n >= len(p.Instrs)) {
			return fmt.Errorf("instr %d: successor %d out of range", i, n)
		}
		return nil
	}
	for i, ins := range p.Instrs {
		switch ins.Op {
		case OpInput:
			if err := checkReg(ins.Dst, "dst", i); err != nil {
				return err
			}
			if ins.Pos < 0 {
				return fmt.Errorf("instr %d: negative input position %d", i, ins.Pos)
			}
		case OpAnd, OpAdd:
			if err := checkReg(ins.Dst, "dst", i); err != nil {
				return err
			}
		case OpJnz:
			if err := checkReg(ins.Src, "src", i); err != nil {
				return err
			}
		case OpLoad:
			if err := checkReg(ins.Dst, "dst", i); err != nil {
				return err
			}
			if err := checkReg(ins.Src, "src", i); err != nil {
				return err
			}
			if ins.Arr < 0 || ins.Arr >= len(p.Arrays) {
				return fmt.Errorf("instr %d: array %d out of range", i, ins.Arr)
			}
		default:
			return fmt.Errorf("instr %d: unknown op %d", i, int(ins.Op))
		}
		for _, n := range nexts(ins) {
			if err := checkNext(n, i); err != nil {
				return err
			}
		}
	}
	// Acyclicity check via DFS coloring from the entry.
	const (
		white = iota
		gray
		black
	)
	color := make([]int, len(p.Instrs))
	var visit func(pc int) error
	visit = func(pc int) error {
		if pc == Halt {
			return nil
		}
		switch color[pc] {
		case gray:
			return fmt.Errorf("instruction graph has a cycle through instr %d", pc)
		case black:
			return nil
		}
		color[pc] = gray
		for _, n := range nexts(p.Instrs[pc]) {
			if err := visit(n); err != nil {
				return err
			}
		}
		color[pc] = black
		return nil
	}
	return visit(p.Entry)
}
