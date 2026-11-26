package seccomp

import "math"

// DefaultPathBudget caps the number of explored symbolic states per binding.
const DefaultPathBudget = 200000

type symVal struct {
	e *Expr
	d Dom
}

func constVal(c uint32) symVal {
	return symVal{e: eConst(c), d: constDom(c)}
}

type symState struct {
	pc    int
	a     symVal
	x     symVal
	mem   [MemWords]symVal
	preds []Pred
	path  []int
}

func (st *symState) clone() symState {
	cp := *st
	cp.preds = append([]Pred(nil), st.preds...)
	cp.path = append([]int(nil), st.path...)
	return cp
}

// Outcome is one symbolic termination of a program.
type Outcome struct {
	// Action is the constant SECCOMP_RET_* value when !Dynamic.
	Action uint32
	// Dynamic marks BPF_RET|BPF_A.
	Dynamic bool
	// ActionExpr models the returned register when Dynamic.
	ActionExpr *Expr
	Preds      []Pred
	Path       []int
}

type explorer struct {
	insns  []Instruction
	seeds  []Dom
	budget int
	nodes  int
	trunc  bool
}

// exploreOutcomes runs path-sensitive DAG exploration starting from seeds.
// It returns all terminal outcomes and the (possibly grown via opaque
// variables) seed domains used for solving.
func exploreOutcomes(insns []Instruction, seeds []Dom, budget int) ([]Outcome, []Dom, int, bool) {
	if budget <= 0 {
		budget = DefaultPathBudget
	}
	ex := &explorer{insns: insns, seeds: append([]Dom(nil), seeds...), budget: budget}
	zero := constVal(0)
	st0 := symState{pc: 0, a: zero, x: zero, preds: nil, path: []int{0}}
	for i := range st0.mem {
		st0.mem[i] = zero
	}

	var outs []Outcome
	stack := []symState{st0}
	for len(stack) > 0 {
		st := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		ex.nodes++
		if ex.nodes > ex.budget {
			ex.trunc = true
			break
		}
		if st.pc < 0 || st.pc >= len(insns) {
			continue
		}
		children, outcomes := ex.step(st)
		outs = append(outs, outcomes...)
		for _, ch := range children {
			stack = append(stack, ch)
		}
	}
	return outs, ex.seeds, ex.nodes, ex.trunc
}

func (ex *explorer) opaqueVal() symVal {
	id := len(ex.seeds)
	ex.seeds = append(ex.seeds, topDom())
	return symVal{e: eVar(id), d: topDom()}
}

func (ex *explorer) bin(op EOp, l, r symVal) symVal {
	return symVal{e: eBin(op, l.e, r.e), d: binDom(op, l.d, r.d)}
}

// loadABS builds an exact little-endian byte-concatenation expression for a
// constant ABS offset and width.
func (ex *explorer) loadABS(off uint32, sizeField uint16) symVal {
	n := loadSize(sizeField)
	if uint64(off)+uint64(n) > uint64(SeccompDataSize) {
		return constVal(0)
	}
	var expr *Expr
	for i := uint32(0); i < n; i++ {
		bo := off + i
		wid := int(bo / 4)
		shift := uint32((bo % 4) * 8)
		by := eBin(EShr, eVar(wid), eConst(shift))
		by = eBin(EAnd, by, eConst(0xff))
		if i > 0 {
			by = eBin(EShl, by, eConst(8*i))
			if expr == nil {
				expr = by
			} else {
				expr = eBin(EOr, expr, by)
			}
		} else {
			expr = by
		}
	}
	return symVal{e: expr, d: evalDom(expr, ex.seeds)}
}

func (ex *explorer) step(st symState) ([]symState, []Outcome) {
	insn := ex.insns[st.pc]
	code := insn.Code
	k := insn.K
	var children []symState
	var outs []Outcome

	gotoNext := func(nst symState) {
		nst.pc = st.pc + 1
		nst.path = append(append([]int(nil), st.path...), nst.pc)
		children = append(children, nst)
	}

	switch code & classMask {
	case ClassLD, ClassLDX:
		sizeField := code & sizeMask
		mode := code & modeMask
		var val symVal
		switch mode {
		case ModeIMM:
			val = constVal(k)
		case ModeABS:
			val = ex.loadABS(k, sizeField)
		case ModeIND:
			if xc, ok := st.x.d.isConst(); ok {
				off := k + xc
				n := loadSize(sizeField)
				if uint64(off)+uint64(n) <= SeccompDataSize {
					val = ex.loadABS(off, sizeField)
				} else {
					val = constVal(0)
				}
			} else {
				val = ex.opaqueVal()
			}
		case ModeMEM:
			if int(k) < MemWords {
				val = st.mem[k]
			} else {
				val = constVal(0)
			}
		case ModeLEN:
			val = constVal(0)
		case ModeMSH:
			b := ex.loadABS(k, SizeB)
			masked := ex.bin(EAnd, b, constVal(0x0f))
			val = ex.bin(EShl, masked, constVal(2))
		}
		nst := st.clone()
		if code&classMask == ClassLD {
			nst.a = val
		} else {
			nst.x = val
		}
		gotoNext(nst)

	case ClassST:
		nst := st.clone()
		if int(k) < MemWords {
			nst.mem[k] = st.a
		}
		gotoNext(nst)
	case ClassSTX:
		nst := st.clone()
		if int(k) < MemWords {
			nst.mem[k] = st.x
		}
		gotoNext(nst)

	case ClassALU:
		src := constVal(k)
		if code&srcMask == SrcX {
			src = st.x
		}
		op := aluEOp(code & aluMask)
		if op == EDiv || op == EMod {
			if !mayEqConst(src.d, 0) {
				nst := st.clone()
				nst.a = ex.bin(op, st.a, src)
				gotoNext(nst)
				break
			}
			// Zero divisor is feasible: kernel returns 0 (KILL_THREAD).
			zeroOut := Outcome{
				Action: RetKillThread,
				Preds:  withPreds(st.preds, Pred{Kind: PredEQ, L: src.e, R: eConst(0)}),
				Path:   append([]int(nil), st.path...),
			}
			outs = append(outs, zeroOut)
			nz := st.clone()
			nz.preds = withPreds(st.preds, Pred{Kind: PredNE, L: src.e, R: eConst(0)})
			nz.a = ex.bin(op, st.a, src)
			gotoNext(nz)
			break
		}
		nst := st.clone()
		if op == ENeg {
			nd := topDom()
			if c, ok := st.a.d.isConst(); ok {
				nd = constDom(-c)
			}
			nst.a = symVal{e: eUnary(ENeg, st.a.e), d: nd}
		} else {
			nst.a = ex.bin(op, st.a, src)
		}
		gotoNext(nst)

	case ClassJMP:
		if code&jmpMask == JmpJA {
			target := JumpTarget(st.pc, int(k))
			nst := st.clone()
			nst.pc = target
			nst.path = append(append([]int(nil), st.path...), target)
			children = append(children, nst)
			break
		}
		var rhs symVal
		if code&srcMask == SrcX {
			rhs = st.x
		} else {
			rhs = constVal(k)
		}
		jop := code & jmpMask
		takePred, fallPred, takeDomOK, fallDomOK := branchFeasibility(jop, st.a, rhs)

		if takeDomOK {
			nst := st.clone()
			nst.preds = withPreds(st.preds, takePred)
			nst.a = tightenOnCompare(jop, st.a, rhs, true)
			target := JumpTarget(st.pc, int(insn.JT))
			nst.pc = target
			nst.path = append(append([]int(nil), st.path...), target)
			children = append(children, nst)
		}
		if fallDomOK {
			nst := st.clone()
			nst.preds = withPreds(st.preds, fallPred)
			nst.a = tightenOnCompare(jop, st.a, rhs, false)
			target := JumpTarget(st.pc, int(insn.JF))
			nst.pc = target
			nst.path = append(append([]int(nil), st.path...), target)
			children = append(children, nst)
		}

	case ClassRET:
		if code == 0x16 || code&0x18 == RetA {
			outs = append(outs, Outcome{
				Dynamic:    true,
				ActionExpr: st.a.e,
				Preds:      append([]Pred(nil), st.preds...),
				Path:       append([]int(nil), st.path...),
			})
		} else {
			outs = append(outs, Outcome{
				Action: insn.K,
				Preds:  append([]Pred(nil), st.preds...),
				Path:   append([]int(nil), st.path...),
			})
		}

	case ClassMISC:
		nst := st.clone()
		switch code & miscMask {
		case MiscTAX:
			nst.x = st.a
		case MiscTXA:
			nst.a = st.x
		}
		gotoNext(nst)
	}
	return children, outs
}

func withPreds(base []Pred, add ...Pred) []Pred {
	out := make([]Pred, 0, len(base)+len(add))
	out = append(out, base...)
	out = append(out, add...)
	return out
}

func aluEOp(op uint16) EOp {
	switch op {
	case AluADD:
		return EAdd
	case AluSUB:
		return ESub
	case AluMUL:
		return EMul
	case AluDIV:
		return EDiv
	case AluMOD:
		return EMod
	case AluOR:
		return EOr
	case AluAND:
		return EAnd
	case AluLSH:
		return EShl
	case AluRSH:
		return EShr
	case AluXOR:
		return EXor
	case AluNEG:
		return ENeg
	}
	return EAdd
}

// branchFeasibility builds the taken/fall predicates and checks their
// abstract feasibility against current register domains.
func branchFeasibility(jop uint16, a, rhs symVal) (take Pred, fall Pred, takeOK, fallOK bool) {
	zero := constVal(0)
	switch jop {
	case JmpJEQ:
		take = Pred{Kind: PredEQ, L: a.e, R: rhs.e}
		fall = Pred{Kind: PredNE, L: a.e, R: rhs.e}
		takeOK = predDomMay(PredEQ, a.d, rhs.d)
		fallOK = predDomMay(PredNE, a.d, rhs.d)
	case JmpJGT:
		take = Pred{Kind: PredUGT, L: a.e, R: rhs.e}
		fall = Pred{Kind: PredULE, L: a.e, R: rhs.e}
		takeOK = predDomMay(PredUGT, a.d, rhs.d)
		fallOK = predDomMay(PredULE, a.d, rhs.d)
	case JmpJGE:
		take = Pred{Kind: PredUGE, L: a.e, R: rhs.e}
		fall = Pred{Kind: PredULT, L: a.e, R: rhs.e}
		takeOK = predDomMay(PredUGE, a.d, rhs.d)
		fallOK = predDomMay(PredULT, a.d, rhs.d)
	case JmpJSET:
		andD := andDom(a.d, rhs.d)
		andE := eBin(EAnd, a.e, rhs.e)
		take = Pred{Kind: PredNE, L: andE, R: zero.e}
		fall = Pred{Kind: PredEQ, L: andE, R: zero.e}
		takeOK = predDomMay(PredNE, andD, zero.d)
		fallOK = predDomMay(PredEQ, andD, zero.d)
	}
	return
}

func predDomMay(kind PredKind, l, r Dom) bool {
	switch kind {
	case PredEQ:
		return mayEqDom(l, r)
	case PredNE:
		if cl, ok := l.isConst(); ok {
			if cr, ok := r.isConst(); ok {
				return cl != cr
			}
		}
		return true
	case PredUGT:
		return l.hi > r.lo
	case PredUGE:
		return l.hi >= r.lo
	case PredULT:
		return l.lo < r.hi
	case PredULE:
		return l.lo <= r.hi
	}
	return true
}

// tightenOnCompare performs sound best-effort domain narrowing of A after a
// comparison edge.  It never changes the expression.
func tightenOnCompare(jop uint16, a symVal, rhs symVal, taken bool) symVal {
	d := a.d
	kc, kConst := rhs.d.isConst()
	switch jop {
	case JmpJEQ:
		if taken {
			if kConst {
				a.d = constDom(kc)
				return a
			}
			if m, ok := meetBits(d, rhs.d); ok {
				a.d = m
			}
		}
	case JmpJGT:
		if !kConst {
			return a
		}
		if taken {
			if kc == math.MaxUint32 {
				return a // infeasible, handled by feasibility
			}
			if d.lo < kc+1 {
				d.lo = kc + 1
			}
		} else {
			if d.hi > kc {
				d.hi = kc
			}
		}
	case JmpJGE:
		if !kConst {
			return a
		}
		if taken {
			if d.lo < kc {
				d.lo = kc
			}
		} else {
			if kc == 0 {
				d.hi = 0 // infeasible ULT 0
			} else if d.hi > kc-1 {
				d.hi = kc - 1
			}
		}
	}
	a.d = d
	return a
}
