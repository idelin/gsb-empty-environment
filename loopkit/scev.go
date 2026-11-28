package loopkit

import "math/big"

// SCEVNode is a scalar-evolution expression node.
type SCEVNode struct {
	Kind  SCEVKind
	Typ   Type
	Value *Instr // backing SSA value for constants/unknowns
	Const int64
	Start *SCEVNode
	Step  *SCEVNode
	Loop  *Loop
}

// SCEVKind enumerates SCEV node shapes.
type SCEVKind int

const (
	SCEVUnknown SCEVKind = iota
	SCEVConstant
	SCEVAddRec
)

func (k SCEVKind) String() string {
	switch k {
	case SCEVConstant:
		return "const"
	case SCEVAddRec:
		return "addrec"
	default:
		return "unknown"
	}
}

// String renders the canonical "{start, +, step}<L>" recurrence form.
func (n *SCEVNode) String() string {
	if n == nil {
		return "<nil>"
	}
	switch n.Kind {
	case SCEVConstant:
		if n.Value != nil && n.Value.Name != "" {
			return n.Value.Name
		}
		return big.NewInt(n.Const).String()
	case SCEVAddRec:
		return "{" + n.Start.String() + ", +, " + n.Step.String() +
			"}<" + n.Loop.Header.Name + ">"
	default:
		if n.Value != nil {
			return n.Value.ValueName()
		}
		return "?"
	}
}

// SCEV is the per-function scalar evolution analysis.
type SCEV struct {
	Forest *LoopForest
	cache  map[*Instr]*SCEVNode
}

// NewSCEV builds the analysis on top of a loop forest.
func NewSCEV(forest *LoopForest) *SCEV {
	return &SCEV{Forest: forest, cache: map[*Instr]*SCEVNode{}}
}

// AnalyzeSCEV is a convenience constructor over a fresh loop analysis.
func AnalyzeSCEV(f *Func) *SCEV {
	return NewSCEV(AnalyzeLoops(f))
}

// Get returns (creating and caching) the SCEV expression for value v.
func (s *SCEV) Get(v *Instr) *SCEVNode {
	if v == nil {
		return &SCEVNode{Kind: SCEVUnknown}
	}
	if n, ok := s.cache[v]; ok {
		return n
	}
	n := s.build(v)
	s.cache[v] = n
	return n
}

func (s *SCEV) build(v *Instr) *SCEVNode {
	switch v.Op {
	case OpConst:
		return &SCEVNode{Kind: SCEVConstant, Typ: v.Typ, Const: v.Int, Value: v}
	case OpPhi:
		if l := s.Forest.LoopAt(v.Block); l != nil && l.Header == v.Block {
			if rec := s.tryAddRec(v, l); rec != nil {
				return rec
			}
		}
	case OpAdd:
		// Non-phi induction update already gets normalized via the phi
		// recurrence; other adds remain unknown here.
	}
	return &SCEVNode{Kind: SCEVUnknown, Typ: v.Typ, Value: v}
}

// tryAddRec recognizes a header phi of shape
// phi(start_outside, backedge_value) with backedge_value = phi + step,
// where start and step are loop invariant.
func (s *SCEV) tryAddRec(phi *Instr, l *Loop) *SCEVNode {
	if len(phi.Operands) != 2 || len(phi.PredBlocks) != 2 {
		return nil
	}
	var start, back *Instr
	for idx, pb := range phi.PredBlocks {
		if l.Blocks[pb] {
			back = phi.Operands[idx]
		} else {
			start = phi.Operands[idx]
		}
	}
	if start == nil || back == nil || back.Op != OpAdd {
		return nil
	}
	x, y := back.Operands[0], back.Operands[1]
	var step *Instr
	switch {
	case x == phi:
		step = y
	case y == phi:
		step = x
	default:
		return nil
	}
	startNode := s.Get(start)
	stepNode := s.Get(step)
	if !s.isInvariant(startNode, l) || !s.isInvariant(stepNode, l) {
		return nil
	}
	return &SCEVNode{
		Kind: SCEVAddRec, Typ: phi.Typ, Value: phi,
		Start: startNode, Step: stepNode, Loop: l,
	}
}

func (s *SCEV) isInvariant(n *SCEVNode, l *Loop) bool {
	if n == nil {
		return false
	}
	if n.Kind == SCEVAddRec {
		return n.Loop != l && !l.Blocks[n.Value.Block]
	}
	if n.Value != nil && l.Blocks[n.Value.Block] {
		return false
	}
	return true
}

// ExitCmp describes the controlling comparison of a loop exit.
type ExitCmp struct {
	Cmp      *Instr
	Ind      *SCEVNode // induction recurrence
	Bound    *SCEVNode // loop-invariant bound
	Pred     Pred      // predicate that, when true, means "loop continues"
	PostTest bool      // comparison lives on a latch (do-while style)
}

// FindExitCmp locates the inductive exit comparison for loop l.
func (s *SCEV) FindExitCmp(l *Loop) *ExitCmp {
	if c := s.findCondExit(l); c != nil {
		return c
	}
	return s.findLatchExit(l)
}

func (s *SCEV) findCondExit(l *Loop) *ExitCmp {
	for _, b := range l.Forest.Dom.RPO() {
		if !l.Blocks[b] || b.Term == nil || b.Term.Op != TermCond {
			continue
		}
		cmp := b.Term.Args[0]
		if cmp.Op != OpICmp {
			continue
		}
		ec := s.classifyCmp(cmp, l)
		if ec == nil {
			continue
		}
		thenB, elseB := b.Term.Succs[0], b.Term.Succs[1]
		inThen, inElse := l.Blocks[thenB], l.Blocks[elseB]
		switch {
		case inThen && !inElse:
			// ec.Pred is already normalized by classifyCmp
		case inElse && !inThen:
			ec.Pred = invertPred(ec.Pred)
		default:
			continue
		}
		return ec
	}
	return nil
}

func (s *SCEV) findLatchExit(l *Loop) *ExitCmp {
	for _, latch := range l.BackEdges {
		if latch.Term == nil || latch.Term.Op != TermCond {
			continue
		}
		cmp := latch.Term.Args[0]
		if cmp.Op != OpICmp {
			continue
		}
		ec := s.classifyCmp(cmp, l)
		if ec == nil {
			continue
		}
		thenB, elseB := latch.Term.Succs[0], latch.Term.Succs[1]
		backThen := thenB == l.Header
		backElse := elseB == l.Header
		switch {
		case backThen && !backElse:
			// keep the normalized predicate
		case backElse && !backThen:
			ec.Pred = invertPred(ec.Pred)
		default:
			continue
		}
		ec.PostTest = true
		return ec
	}
	return nil
}

func (s *SCEV) classifyCmp(cmp *Instr, l *Loop) *ExitCmp {
	a, b := s.Get(cmp.Operands[0]), s.Get(cmp.Operands[1])
	if a.Kind == SCEVAddRec && a.Loop == l && s.isInvariant(b, l) {
		return &ExitCmp{Cmp: cmp, Ind: a, Bound: b, Pred: cmp.Pred}
	}
	if b.Kind == SCEVAddRec && b.Loop == l && s.isInvariant(a, l) {
		return &ExitCmp{Cmp: cmp, Ind: b, Bound: a, Pred: swapPred(cmp.Pred)}
	}
	return nil
}

func invertPred(p Pred) Pred {
	switch p {
	case PredEQ:
		return PredNE
	case PredNE:
		return PredEQ
	case PredUGT:
		return PredULE
	case PredUGE:
		return PredULT
	case PredULT:
		return PredUGE
	case PredULE:
		return PredUGT
	case PredSGT:
		return PredSLE
	case PredSGE:
		return PredSLT
	case PredSLT:
		return PredSGE
	case PredSLE:
		return PredSGT
	}
	return p
}

func swapPred(p Pred) Pred {
	switch p {
	case PredUGT:
		return PredULT
	case PredUGE:
		return PredULE
	case PredULT:
		return PredUGT
	case PredULE:
		return PredUGE
	case PredSGT:
		return PredSLT
	case PredSGE:
		return PredSLE
	case PredSLT:
		return PredSGT
	case PredSLE:
		return PredSGE
	}
	return p // EQ/NE are symmetric
}
