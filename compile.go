package packetvm

// label is a symbolic jump target resolved once emission is complete.
type label struct{ id int }

// jumpSlot records an emitted jump and the symbolic label it must reach.
type jumpSlot struct {
	slot int // instruction index of the jump
	to   label
}

// builder accumulates instructions and label placements. Labels are stored
// separately from the instruction stream, so jump slots always hold a valid
// final instruction index.
type builder struct {
	insns []Insn
	labs  []labelAt
	refs  []jumpSlot
	labID int
}

type labelAt struct {
	lab label
	pc  int // instruction index the label is placed before
}

func (b *builder) newLabel() label {
	id := b.labID
	b.labID++
	return label{id: id}
}

func (b *builder) place(l label) {
	b.labs = append(b.labs, labelAt{lab: l, pc: len(b.insns)})
}

func (b *builder) emit(in Insn) {
	b.insns = append(b.insns, in)
}

func (b *builder) jump(to label, in Insn) {
	b.refs = append(b.refs, jumpSlot{slot: len(b.insns), to: to})
	b.emit(in)
}

// compile lowers a rule to unresolved instructions plus jump references.
//
// Every jump is emitted before its target label is placed, so after
// resolution all control transfers are strictly forward.
func compile(rule Rule) ([]Insn, []jumpSlot, error) {
	b := &builder{}

	var emitBool func(x Bool, onTrue, onFalse label)
	var emitNum func(n Numeric, dst Reg)

	emitNum = func(n Numeric, dst Reg) {
		switch v := n.(type) {
		case *Imm:
			b.emit(Insn{Op: OpImm, Imm: v.Value, Dst: dst, Origin: v.Pos})
		case *Len:
			b.emit(Insn{Op: OpLen, Dst: dst, Origin: v.Pos})
		case *Load:
			b.emit(Insn{Op: OpLoad, Offset: v.Offset, Size: v.Size, Dst: dst, Origin: v.Pos})
		default:
			panic("packetvm: unknown numeric node")
		}
	}

	// emitCmp evaluates the comparison and jumps to onTrue when it holds,
	// falling through to the next instruction otherwise.
	emitCmp := func(c *Cmp, onTrue label) {
		lhs, op, rhs := c.LHS, c.Op, c.RHS
		// Normalize so a constant compared against len/load is moved to the
		// right-hand side: (k op x) == (x op^-1 k). This keeps the packet
		// length in R0, which the verifier tracks for length proofs.
		if _, lhsImm := lhs.(*Imm); lhsImm {
			if _, rhsImm := rhs.(*Imm); !rhsImm {
				lhs, op, rhs = rhs, c.Op.negated(), lhs
			}
		}
		emitNum(lhs, R0)
		rimm, isImm := rhs.(*Imm)
		if isImm {
			b.jump(onTrue, Insn{Op: OpJmpIf, Cond: op, Imm: rimm.Value, Origin: c.Pos})
			return
		}
		emitNum(rhs, R1)
		b.jump(onTrue, Insn{Op: OpJmpIf, Cond: op, UseReg: true, Origin: c.Pos})
	}

	emitBool = func(x Bool, onTrue, onFalse label) {
		switch v := x.(type) {
		case *Cmp:
			emitCmp(v, onTrue)
			b.jump(onFalse, Insn{Op: OpJmp, Origin: v.Pos})
		case *Not:
			emitBool(v.Inner, onFalse, onTrue)
		case *And:
			for i, t := range v.Terms {
				if i == len(v.Terms)-1 {
					emitBool(t, onTrue, onFalse)
					continue
				}
				next := b.newLabel()
				emitBool(t, next, onFalse)
				b.place(next)
			}
		case *Or:
			for i, t := range v.Terms {
				if i == len(v.Terms)-1 {
					emitBool(t, onTrue, onFalse)
					continue
				}
				next := b.newLabel()
				emitBool(t, onTrue, next)
				b.place(next)
			}
		default:
			panic("packetvm: unknown boolean node")
		}
	}

	trueLab := b.newLabel()
	falseLab := b.newLabel()
	if rule.Body != nil {
		emitBool(rule.Body, trueLab, falseLab)
	}
	b.place(trueLab)
	b.emit(Insn{Op: OpRet, Verdict: true})
	b.place(falseLab)
	b.emit(Insn{Op: OpRet, Verdict: false})

	positions := map[int]int{}
	for _, l := range b.labs {
		positions[l.lab.id] = l.pc
	}
	for _, r := range b.refs {
		target, ok := positions[r.to.id]
		if !ok {
			return nil, nil, &RejectError{Kind: RejectDanglingJump, Index: r.slot}
		}
		off := target - r.slot
		if off <= 0 {
			return nil, nil, &RejectError{
				Kind:   RejectBackwardJump,
				Source: b.insns[r.slot].Origin,
				Index:  r.slot,
			}
		}
		b.insns[r.slot].Off = uint32(off)
	}
	return b.insns, b.refs, nil
}
