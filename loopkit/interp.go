package loopkit

import (
	"fmt"
)

// TrapReason identifies the runtime failure category.
type TrapReason int

const (
	TrapDivByZero TrapReason = iota
	TrapDivOverflow
	TrapStepLimit
)

// Trap is a runtime failure raised while interpreting.
type Trap struct {
	Reason TrapReason
	Instr  *Instr
}

func (t *Trap) Error() string {
	switch t.Reason {
	case TrapDivByZero:
		return "trap: division by zero at " + t.Instr.ValueName()
	case TrapDivOverflow:
		return "trap: signed division overflow at " + t.Instr.ValueName()
	case TrapStepLimit:
		return "trap: step limit reached (possible infinite loop)"
	}
	return "trap"
}

// HostFunc implements a callable external function.
type HostFunc func(args []int64) (int64, error)

// Result captures one interpreted execution.
type Result struct {
	Ret   int64
	Void  bool
	Trap  *Trap
	Steps int64
}

type cell struct {
	val int64
}

// Machine interprets one function with fixed-width integer semantics.
type Machine struct {
	Hosts   map[string]HostFunc
	MaxStep int64
	mem     map[*Instr]map[int64]*cell // alloca -> offset -> cell
	values  map[*Instr]int64
	steps   int64
}

// NewMachine creates an interpreter; 0 MaxStep means a safe default.
func NewMachine() *Machine {
	return &Machine{
		Hosts:   map[string]HostFunc{},
		MaxStep: 1_000_000,
		mem:     map[*Instr]map[int64]*cell{},
		values:  map[*Instr]int64{},
	}
}

// Run executes f with the given integer arguments.
func (m *Machine) Run(f *Func, args ...int64) Result {
	m.mem = map[*Instr]map[int64]*cell{}
	m.values = map[*Instr]int64{}
	m.steps = 0
	for idx, p := range f.Params {
		if idx < len(args) {
			m.values[p] = args[idx]
		}
	}
	cur := f.Entry()
	for {
		var next *Block
		var res Result
		cur, next, res = m.stepBlock(f, cur)
		if res.Trap != nil {
			res.Steps = m.steps
			return res
		}
		if res.Void || cur == nil {
			res.Steps = m.steps
			return res
		}
		_ = next
	}
}

func (m *Machine) stepBlock(f *Func, b *Block) (*Block, *Block, Result) {
	// Phi values are supplied by the predecessor before branching; the
	// interpreter stores them eagerly when the terminator is taken.
	for _, i := range b.Instrs {
		m.steps++
		if m.MaxStep > 0 && m.steps > m.MaxStep {
			return nil, nil, Result{Trap: &Trap{Reason: TrapStepLimit}}
		}
		if i.Op == OpPhi {
			continue // already populated on edge traversal
		}
		v, trap := m.eval(i)
		if trap != nil {
			return nil, nil, Result{Trap: trap}
		}
		m.values[i] = v
	}
	switch b.Term.Op {
	case TermRet:
		if len(b.Term.Args) == 0 {
			return nil, nil, Result{Void: true}
		}
		return nil, nil, Result{Ret: m.values[b.Term.Args[0]]}
	case TermBr:
		target := b.Term.Succs[0]
		m.fillPhis(b, target)
		return target, target, Result{}
	case TermCond:
		cond := m.values[b.Term.Args[0]]
		var target *Block
		if cond != 0 {
			target = b.Term.Succs[0]
		} else {
			target = b.Term.Succs[1]
		}
		m.fillPhis(b, target)
		return target, target, Result{}
	}
	return nil, nil, Result{Trap: &Trap{Reason: TrapStepLimit}}
}

func (m *Machine) fillPhis(from, to *Block) {
	for _, phi := range to.Phis() {
		for idx, pb := range phi.PredBlocks {
			if pb == from {
				m.values[phi] = m.values[phi.Operands[idx]]
				break
			}
		}
	}
}

func (m *Machine) eval(i *Instr) (int64, *Trap) {
	switch i.Op {
	case OpConst:
		return i.Int, nil
	case OpParam:
		return m.values[i], nil
	case OpAdd:
		return wrapBin(i, m.values[i.Operands[0]]+m.values[i.Operands[1]]), nil
	case OpSub:
		return wrapBin(i, m.values[i.Operands[0]]-m.values[i.Operands[1]]), nil
	case OpMul:
		return wrapMul(i, m.values[i.Operands[0]], m.values[i.Operands[1]]), nil
	case OpUDiv, OpURem:
		return m.evalUnsignedDiv(i)
	case OpSDiv, OpSRem:
		return m.evalSignedDiv(i)
	case OpAnd:
		return m.values[i.Operands[0]] & m.values[i.Operands[1]], nil
	case OpOr:
		return m.values[i.Operands[0]] | m.values[i.Operands[1]], nil
	case OpXor:
		return m.values[i.Operands[0]] ^ m.values[i.Operands[1]], nil
	case OpShl:
		return wrapBin(i, m.values[i.Operands[0]]<<
			uint(m.values[i.Operands[1]]&63)), nil
	case OpLShr:
		return int64(uint64(m.values[i.Operands[0]]) >>
			uint(m.values[i.Operands[1]]&63)), nil
	case OpAShr:
		return m.values[i.Operands[0]] >>
			uint(m.values[i.Operands[1]]&63), nil
	case OpICmp:
		return m.evalCmp(i), nil
	case OpLoad:
		return m.load(i), nil
	case OpStore:
		m.doStore(i)
		return 0, nil
	case OpGEP:
		return m.values[i.Operands[0]], nil // pointer identity preserved
	case OpAlloca:
		return 0, nil
	case OpCall:
		return m.evalCall(i)
	}
	return 0, &Trap{Instr: i, Reason: TrapStepLimit}
}

func (m *Machine) evalUnsignedDiv(i *Instr) (int64, *Trap) {
	x, y := uint64(m.values[i.Operands[0]]), uint64(m.values[i.Operands[1]])
	if y == 0 {
		return 0, &Trap{Reason: TrapDivByZero, Instr: i}
	}
	if i.Op == OpUDiv {
		return int64(x / y), nil
	}
	return int64(x % y), nil
}

func (m *Machine) evalSignedDiv(i *Instr) (int64, *Trap) {
	x, y := m.values[i.Operands[0]], m.values[i.Operands[1]]
	if y == 0 {
		return 0, &Trap{Reason: TrapDivByZero, Instr: i}
	}
	bits := uint(i.Typ.Bits())
	min := int64(-1) << (bits - 1)
	if x == min && y == -1 {
		return 0, &Trap{Reason: TrapDivOverflow, Instr: i}
	}
	if i.Op == OpSDiv {
		return x / y, nil
	}
	return x % y, nil
}

func (m *Machine) evalCmp(i *Instr) int64 {
	a, b := m.values[i.Operands[0]], m.values[i.Operands[1]]
	ua, ub := uint64(a), uint64(b)
	var ok bool
	switch i.Pred {
	case PredEQ:
		ok = a == b
	case PredNE:
		ok = a != b
	case PredUGT:
		ok = ua > ub
	case PredUGE:
		ok = ua >= ub
	case PredULT:
		ok = ua < ub
	case PredULE:
		ok = ua <= ub
	case PredSGT:
		ok = a > b
	case PredSGE:
		ok = a >= b
	case PredSLT:
		ok = a < b
	case PredSLE:
		ok = a <= b
	}
	if ok {
		return 1
	}
	return 0
}

func (m *Machine) load(i *Instr) int64 {
	base, off := m.address(i.Operands[0])
	bucket := m.mem[base]
	if bucket == nil {
		return 0
	}
	if c := bucket[off]; c != nil {
		return c.val
	}
	return 0
}

func (m *Machine) doStore(i *Instr) {
	addr, val := i.Operands[0], i.Operands[1]
	base, off := m.address(addr)
	if m.mem[base] == nil {
		m.mem[base] = map[int64]*cell{}
	}
	c := m.mem[base][off]
	if c == nil {
		c = &cell{}
		m.mem[base][off] = c
	}
	c.val = wrapBin(val, m.values[val])
}

// address walks the GEP chain and returns the alloca plus element offset.
func (m *Machine) address(addr *Instr) (*Instr, int64) {
	off := int64(0)
	for addr.Op == OpGEP {
		off += addr.GepIdx
		addr = addr.Operands[0]
	}
	return addr, off
}

func (m *Machine) evalCall(i *Instr) (int64, *Trap) {
	fn := m.Hosts[i.Callee]
	if fn == nil {
		return 0, &Trap{Instr: i, Reason: TrapStepLimit}
	}
	args := make([]int64, len(i.Operands))
	for idx, op := range i.Operands {
		args[idx] = m.values[op]
	}
	v, err := fn(args)
	if err != nil {
		return 0, &Trap{Instr: i, Reason: TrapStepLimit}
	}
	return v, nil
}

func wrapBin(i *Instr, v int64) int64 {
	bits := uint(i.Typ.Bits())
	if bits == 0 || bits >= 64 {
		return v
	}
	return v << (64 - bits) >> (64 - bits)
}

func wrapMul(i *Instr, a, b int64) int64 {
	bits := uint(i.Typ.Bits())
	if bits == 0 || bits >= 64 {
		return a * b
	}
	mod := uint64(1) << bits
	return int64(uint64(a) * uint64(b) % mod)
}

// EquivError describes a semantic mismatch between two executions.
type EquivError struct{ Msg string }

func (e *EquivError) Error() string { return "equivalence: " + e.Msg }

// AssertEquivalent runs original and optimized functions on the same
// inputs and requires identical return values (or identical traps).
func AssertEquivalent(mach *Machine, orig, opt *Func, args ...int64) error {
	r1 := mach.Run(orig, args...)
	r2 := mach.Run(opt, args...)
	if (r1.Trap == nil) != (r2.Trap == nil) {
		return &EquivError{fmt.Sprintf("args %v: trap mismatch %+v vs %+v",
			args, r1.Trap, r2.Trap)}
	}
	if r1.Trap != nil && r1.Trap.Reason != r2.Trap.Reason {
		return &EquivError{fmt.Sprintf("args %v: trap kind %v vs %v",
			args, r1.Trap.Reason, r2.Trap.Reason)}
	}
	if r1.Void != r2.Void || r1.Ret != r2.Ret {
		return &EquivError{fmt.Sprintf("args %v: result %+v vs %+v",
			args, r1, r2)}
	}
	return nil
}
