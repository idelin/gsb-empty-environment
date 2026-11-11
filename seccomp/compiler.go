package seccomp

import (
	"errors"
	"fmt"
	"math"
)

// maxProgramLen is the classic BPF instruction limit (BPF_MAXINSNS).
const maxProgramLen = 4096

// maxCondJump is the largest forward span encodable in the 8-bit jt/jf
// fields of a conditional jump.
const maxCondJump = 255

// label is a symbolic jump target resolved by the assembler. The field
// forces a non-zero size: distinct zero-sized variables may share an
// address, which would collapse distinct labels into one map key.
type label struct{ id int }

// asmInsn is an instruction with symbolic jump targets. A nil jtL/jfL on a
// conditional jump means fall through to the next instruction.
type asmInsn struct {
	code uint16
	k    uint32
	jtL  *label
	jfL  *label
	jaL  *label
	defs []*label
}

type assembler struct {
	prog    []asmInsn
	pending []*label
}

// mark binds l to the next emitted instruction.
func (a *assembler) mark(l *label) { a.pending = append(a.pending, l) }

func (a *assembler) emit(in asmInsn) {
	in.defs = a.pending
	a.pending = nil
	a.prog = append(a.prog, in)
}

func ldw(off uint32) asmInsn { return asmInsn{code: opLdWAbs, k: off} }
func andK(v uint32) asmInsn  { return asmInsn{code: opAndK, k: v} }
func jeq(v uint32, jt, jf *label) asmInsn {
	return asmInsn{code: opJeqK, k: v, jtL: jt, jfL: jf}
}
func jgt(v uint32, jt, jf *label) asmInsn {
	return asmInsn{code: opJgtK, k: v, jtL: jt, jfL: jf}
}
func jge(v uint32, jt, jf *label) asmInsn {
	return asmInsn{code: opJgeK, k: v, jtL: jt, jfL: jf}
}
func ja(l *label) asmInsn  { return asmInsn{code: opJa, jaL: l} }
func ret(v uint32) asmInsn { return asmInsn{code: opRetK, k: v} }

func isCond(code uint16) bool {
	return code == opJeqK || code == opJgtK || code == opJgeK
}

// Compile validates the policy and lowers it to a classic BPF program.
// The program returns ActionAllow for matched syscalls and ActionReject
// otherwise. Unknown architectures are always rejected.
func Compile(p *Policy) ([]Instruction, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}

	a := &assembler{}
	lReject, lAllow := &label{}, &label{}

	// Architecture gate: anything but the policy's arch is rejected.
	lDispatch := &label{}
	a.emit(ldw(offArch))
	a.emit(jeq(uint32(p.Arch), lDispatch, lReject))

	// Dispatch on the syscall number, one conditional jump per group of
	// rules sharing a number.
	a.mark(lDispatch)
	a.emit(ldw(offNr))
	groups := groupRules(p.Rules)
	for _, g := range groups {
		a.emit(jeq(uint32(g.nr), g.l, nil))
	}
	a.emit(ja(lReject))

	// Per-rule argument checks. A failing rule falls through to the next
	// rule with the same number, or to the reject block.
	for _, g := range groups {
		for j := range g.rules {
			if j == 0 {
				a.mark(g.l)
			}
			fail := lReject
			if j < len(g.rules)-1 {
				fail = &label{}
			}
			for _, c := range g.rules[j].Constraints {
				switch c := c.(type) {
				case MaskedEqual:
					a.emitMaskedEqual(c, fail)
				case InRange:
					a.emitInRange(c, fail)
				}
			}
			a.emit(ja(lAllow))
			if fail != lReject {
				a.mark(fail)
			}
		}
	}

	a.mark(lReject)
	a.emit(ret(ActionReject))
	a.mark(lAllow)
	a.emit(ret(ActionAllow))

	prog, err := resolve(a.prog)
	if err != nil {
		return nil, err
	}
	if len(prog) > maxProgramLen {
		return nil, fmt.Errorf("seccomp: program too large: %d instructions (max %d)",
			len(prog), maxProgramLen)
	}
	return prog, nil
}

type ruleGroup struct {
	nr    int
	rules []Rule
	l     *label
}

// groupRules groups rules by syscall number, preserving first-appearance
// order of both groups and rules within a group.
func groupRules(rules []Rule) []*ruleGroup {
	var groups []*ruleGroup
	byNR := make(map[int]*ruleGroup)
	for _, r := range rules {
		g, ok := byNR[r.NR]
		if !ok {
			g = &ruleGroup{nr: r.NR, l: &label{}}
			byNR[r.NR] = g
			groups = append(groups, g)
		}
		g.rules = append(g.rules, r)
	}
	return groups
}

// emitMaskedEqual emits (arg & mask) == value as two 32-bit word checks.
// Words fully covered by a zero mask are skipped.
func (a *assembler) emitMaskedEqual(c MaskedEqual, fail *label) {
	loMask, hiMask := uint32(c.Mask), uint32(c.Mask>>32)
	loVal, hiVal := uint32(c.Value), uint32(c.Value>>32)
	if loMask != 0 {
		a.emit(ldw(argLoOff(c.Arg)))
		if loMask != math.MaxUint32 {
			a.emit(andK(loMask))
		}
		a.emit(jeq(loVal, nil, fail))
	}
	if hiMask != 0 {
		a.emit(ldw(argHiOff(c.Arg)))
		if hiMask != math.MaxUint32 {
			a.emit(andK(hiMask))
		}
		a.emit(jeq(hiVal, nil, fail))
	}
}

// emitInRange emits an unsigned Min <= arg <= Max check on a 64-bit
// argument, decomposed into high/low 32-bit word comparisons so that the
// carry between words is handled exactly.
func (a *assembler) emitInRange(c InRange, fail *label) {
	if c.Min == 0 && c.Max == math.MaxUint64 {
		return
	}
	loLo, loHi := uint32(c.Min), uint32(c.Min>>32)
	hiLo, hiHi := uint32(c.Max), uint32(c.Max>>32)

	a.emit(ldw(argHiOff(c.Arg)))
	if hiHi != math.MaxUint32 {
		a.emit(jgt(hiHi, fail, nil)) // high word above range: fail
	}
	if loHi != 0 {
		a.emit(jge(loHi, nil, fail)) // high word below range: fail
	}
	// Now loHi <= high word <= hiHi.
	if loHi == hiHi {
		// The bounds share one high word; the low word must lie
		// between the two boundary low words.
		a.emit(ldw(argLoOff(c.Arg)))
		if hiLo != math.MaxUint32 {
			a.emit(jgt(hiLo, fail, nil))
		}
		if loLo != 0 {
			a.emit(jge(loLo, nil, fail))
		}
		return
	}
	// loHi < hiHi: the low word is constrained only when the high word
	// sits exactly on a boundary.
	needLo := loLo != 0
	needHi := hiLo != math.MaxUint32
	lPass := &label{}
	var lChkLo, lChkHi *label
	if needLo {
		lChkLo = &label{}
		a.emit(jeq(loHi, lChkLo, nil))
	}
	if needHi {
		lChkHi = &label{}
		a.emit(jeq(hiHi, lChkHi, nil))
	}
	a.emit(ja(lPass)) // strictly between the boundary high words
	if needHi {
		a.mark(lChkHi)
		a.emit(ldw(argLoOff(c.Arg)))
		a.emit(jgt(hiLo, fail, nil))
		a.emit(ja(lPass))
	}
	if needLo {
		a.mark(lChkLo)
		a.emit(ldw(argLoOff(c.Arg)))
		a.emit(jge(loLo, lPass, fail))
	}
	a.mark(lPass)
}

// resolve lowers symbolic labels to numeric offsets. Conditional jumps
// whose target is more than 255 instructions away are rewritten as a short
// conditional jump over unconditional far trampolines (JA uses a 32-bit
// offset). The rewrite strictly reduces the number of out-of-range
// conditional edges per pass, so it terminates.
func resolve(prog []asmInsn) ([]Instruction, error) {
	for {
		pos, err := labelPositions(prog)
		if err != nil {
			return nil, err
		}
		var out []asmInsn
		var pending []*label
		emit := func(in asmInsn) {
			in.defs = append(in.defs, pending...)
			pending = nil
			out = append(out, in)
		}
		changed := false
		for i, in := range prog {
			if !isCond(in.code) {
				emit(in)
				continue
			}
			jt, err := condOffset(pos, in.jtL, i)
			if err != nil {
				return nil, err
			}
			jf, err := condOffset(pos, in.jfL, i)
			if err != nil {
				return nil, err
			}
			if jt <= maxCondJump && jf <= maxCondJump {
				emit(in)
				continue
			}
			// Far target: both edges of the replacement jump to
			// adjacent instructions (offsets 0 and 1), each either a
			// trampoline to the original target or the fall-through.
			changed = true
			l0, l1 := &label{}, &label{}
			emit(asmInsn{code: in.code, k: in.k, jtL: l0, jfL: l1})
			var after []*label // labels bound past the expansion block
			if in.jtL != nil {
				emit(asmInsn{code: opJa, jaL: in.jtL, defs: []*label{l0}})
			} else {
				after = append(after, l0)
			}
			if in.jfL != nil {
				emit(asmInsn{code: opJa, jaL: in.jfL, defs: []*label{l1}})
			} else {
				after = append(after, l1)
			}
			pending = append(pending, after...)
		}
		if len(pending) != 0 {
			return nil, errors.New("seccomp: internal: unbound label at end of program")
		}
		if !changed {
			return assemble(out)
		}
		prog = out
	}
}

func labelPositions(prog []asmInsn) (map[*label]int, error) {
	pos := make(map[*label]int)
	for i, in := range prog {
		for _, l := range in.defs {
			pos[l] = i
		}
	}
	return pos, nil
}

// condOffset returns the forward offset from instruction i to label l,
// where a nil label means fall through (offset 0).
func condOffset(pos map[*label]int, l *label, i int) (int, error) {
	if l == nil {
		return 0, nil
	}
	p, ok := pos[l]
	if !ok {
		return 0, errors.New("seccomp: internal: undefined label")
	}
	off := p - (i + 1)
	if off < 0 {
		return 0, errors.New("seccomp: internal: backward conditional jump")
	}
	return off, nil
}

func assemble(prog []asmInsn) ([]Instruction, error) {
	pos, err := labelPositions(prog)
	if err != nil {
		return nil, err
	}
	out := make([]Instruction, len(prog))
	for i, in := range prog {
		switch {
		case isCond(in.code):
			jt, err := condOffset(pos, in.jtL, i)
			if err != nil {
				return nil, err
			}
			jf, err := condOffset(pos, in.jfL, i)
			if err != nil {
				return nil, err
			}
			if jt > maxCondJump || jf > maxCondJump {
				return nil, errors.New("seccomp: internal: unresolved far conditional jump")
			}
			out[i] = Instruction{Code: in.code, JT: uint8(jt), JF: uint8(jf), K: in.k}
		case in.code == opJa:
			p, ok := pos[in.jaL]
			if !ok {
				return nil, errors.New("seccomp: internal: undefined label")
			}
			off := p - (i + 1)
			if off < 0 {
				return nil, errors.New("seccomp: internal: backward jump")
			}
			out[i] = Instruction{Code: in.code, K: uint32(off)}
		default:
			out[i] = Instruction{Code: in.code, K: in.k}
		}
	}
	return out, nil
}
