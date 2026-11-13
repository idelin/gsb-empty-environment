package netsec

import "fmt"

// Bounds of the analysis host.
const (
	MaxInstrs     = 32 // max instructions per program
	NumRegs       = 4  // boolean registers, all false at reset
	MaxPubBits    = 8  // max public input bits
	MaxSteps      = 64 // step bound; exceeding it cannot be reported safe
	MaxCandidates = 8  // max guard insertion positions offered by the host
)

// Op is an instruction opcode.
type Op int

const (
	OpReadPub Op = iota // regs[A] = public input bit B
	OpReadVer           // regs[A] = host-given verification result bit
	OpAnd               // regs[A] = regs[A] && regs[B]
	OpNot               // regs[A] = !regs[A]
	OpCJmp              // if regs[A] then pc = B else pc++
	OpUnlock            // unlock event
	OpHalt              // halt
	// OpGuard is the atomic patch instruction "if verification result is
	// false then halt". It is inserted by InsertGuards and rejected in
	// host-supplied programs.
	OpGuard
)

// Instr is one instruction. A and B are operand slots whose meaning
// depends on Op; unused slots are ignored.
type Instr struct {
	Op Op
	A  int
	B  int
}

// Constructors keep programs readable.

func ReadPub(reg, bit int) Instr { return Instr{Op: OpReadPub, A: reg, B: bit} }
func ReadVer(reg int) Instr      { return Instr{Op: OpReadVer, A: reg} }
func And(dst, src int) Instr     { return Instr{Op: OpAnd, A: dst, B: src} }
func Not(reg int) Instr          { return Instr{Op: OpNot, A: reg} }
func CJmp(reg, target int) Instr { return Instr{Op: OpCJmp, A: reg, B: target} }
func Unlock() Instr              { return Instr{Op: OpUnlock} }
func Halt() Instr                { return Instr{Op: OpHalt} }
func Guard() Instr               { return Instr{Op: OpGuard} }

// Prog is a host program under analysis.
type Prog struct {
	Code   []Instr
	NumPub int // number of public input bits, 0..MaxPubBits
}

// Validate rejects programs with illegal size, registers, jump targets,
// public bit indices, or embedded guard instructions.
func (p Prog) Validate() error {
	if len(p.Code) == 0 || len(p.Code) > MaxInstrs {
		return fmt.Errorf("netsec: program length %d out of range [1,%d]", len(p.Code), MaxInstrs)
	}
	if p.NumPub < 0 || p.NumPub > MaxPubBits {
		return fmt.Errorf("netsec: public input bits %d out of range [0,%d]", p.NumPub, MaxPubBits)
	}
	for i, ins := range p.Code {
		if err := p.checkInstr(i, ins); err != nil {
			return err
		}
	}
	return nil
}

func (p Prog) checkInstr(i int, ins Instr) error {
	reg := func(r int) error {
		if r < 0 || r >= NumRegs {
			return fmt.Errorf("netsec: instruction %d: illegal register %d", i, r)
		}
		return nil
	}
	switch ins.Op {
	case OpReadPub:
		if err := reg(ins.A); err != nil {
			return err
		}
		if ins.B < 0 || ins.B >= p.NumPub {
			return fmt.Errorf("netsec: instruction %d: public input bit %d out of range [0,%d)", i, ins.B, p.NumPub)
		}
	case OpReadVer, OpNot:
		if err := reg(ins.A); err != nil {
			return err
		}
	case OpAnd:
		if err := reg(ins.A); err != nil {
			return err
		}
		if err := reg(ins.B); err != nil {
			return err
		}
	case OpCJmp:
		if err := reg(ins.A); err != nil {
			return err
		}
		// Target len(Code) is legal: control flow past the last
		// instruction halts.
		if ins.B < 0 || ins.B > len(p.Code) {
			return fmt.Errorf("netsec: instruction %d: illegal jump target %d", i, ins.B)
		}
	case OpUnlock, OpHalt:
	case OpGuard:
		return fmt.Errorf("netsec: instruction %d: guard is a patch-only instruction", i)
	default:
		return fmt.Errorf("netsec: instruction %d: unknown opcode %d", i, int(ins.Op))
	}
	return nil
}

// validateCandidates checks the host-offered guard insertion positions.
func validateCandidates(p Prog, candidates []int) error {
	if len(candidates) > MaxCandidates {
		return fmt.Errorf("netsec: %d candidate positions exceed limit %d", len(candidates), MaxCandidates)
	}
	seen := make(map[int]bool, len(candidates))
	for _, c := range candidates {
		if c < 0 || c > len(p.Code) {
			return fmt.Errorf("netsec: candidate position %d out of range [0,%d]", c, len(p.Code))
		}
		if seen[c] {
			return fmt.Errorf("netsec: duplicate candidate position %d", c)
		}
		seen[c] = true
	}
	return nil
}

// InsertGuards returns a copy of code with a guard instruction inserted
// before each given position (position len(code) appends after the last
// instruction). Conditional jump targets are remapped so they keep
// referring to the same original instruction; a target past the last
// instruction still means halt.
func InsertGuards(code []Instr, positions []int) []Instr {
	pos := sortedCopy(positions)
	shift := func(target int) int {
		n := 0
		for _, q := range pos {
			if q <= target {
				n++
			}
		}
		return n
	}
	out := make([]Instr, 0, len(code)+len(pos))
	for i := 0; i <= len(code); i++ {
		for _, q := range pos {
			if q == i {
				out = append(out, Guard())
			}
		}
		if i < len(code) {
			ins := code[i]
			if ins.Op == OpCJmp {
				ins.B += shift(ins.B)
			}
			out = append(out, ins)
		}
	}
	return out
}

// combinations yields all size-k subsets of items (ascending) in
// lexicographic order.
func combinations(items []int, k int) [][]int {
	var res [][]int
	var cur []int
	var rec func(start int)
	rec = func(start int) {
		if len(cur) == k {
			res = append(res, append([]int{}, cur...))
			return
		}
		for i := start; i <= len(items)-(k-len(cur)); i++ {
			cur = append(cur, items[i])
			rec(i + 1)
			cur = cur[:len(cur)-1]
		}
	}
	rec(0)
	return res
}

// comparePositions orders position sequences lexicographically.
func comparePositions(a, b []int) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] - b[i]
		}
	}
	return len(a) - len(b)
}

func sortedCopy(s []int) []int {
	out := append([]int(nil), s...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
