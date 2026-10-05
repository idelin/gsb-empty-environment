package sccp

import (
	"fmt"
	"strings"
)

// ConstFold records one instruction replaced by a constant.
type ConstFold struct {
	Block int
	Inst  int
	Dst   string
	Value uint8
	Why   string
}

// EdgeCut records one control-flow edge removed from the graph.
// An edge is cut ONLY on positive proof: a constant branch condition
// selecting the other successor, or a predecessor that provably traps.
// Edges that were simply never visited are reported nowhere.
type EdgeCut struct {
	From, To int
	Why      string
}

// BlockRemoval records one block dropped because no feasible path
// from the entry reaches it.
type BlockRemoval struct {
	Block int
	Why   string
}

// Report explains every transformation applied by Optimize.
type Report struct {
	Folds    []ConstFold
	EdgeCuts []EdgeCut
	Removals []BlockRemoval
}

func (r *Report) String() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "constant replacements (%d):\n", len(r.Folds))
	for _, f := range r.Folds {
		fmt.Fprintf(&sb, "  b%d: %s = %d  (%s)\n", f.Block, f.Dst, f.Value, f.Why)
	}
	fmt.Fprintf(&sb, "edge deletions (%d):\n", len(r.EdgeCuts))
	for _, e := range r.EdgeCuts {
		fmt.Fprintf(&sb, "  b%d -> b%d  (%s)\n", e.From, e.To, e.Why)
	}
	fmt.Fprintf(&sb, "unreachable blocks removed (%d):\n", len(r.Removals))
	for _, b := range r.Removals {
		fmt.Fprintf(&sb, "  b%d  (%s)\n", b.Block, b.Why)
	}
	return sb.String()
}

// foldReason explains why an instruction folds to a constant, using the
// abstract operand values from the block's exit environment computation.
func foldReason(in Inst, va, vb Lat, v Lat) string {
	switch in.Op {
	case OpCopy:
		return fmt.Sprintf("%s is constant %d", in.A, va.Val)
	case OpAdd, OpSub, OpMul:
		return fmt.Sprintf("%d %s %d wraps to %d (mod 256)",
			va.Val, opSym(in.Op), vb.Val, v.Val)
	case OpDiv, OpMod:
		return fmt.Sprintf("%d %s %d = %d, divisor non-zero",
			va.Val, opSym(in.Op), vb.Val, v.Val)
	default:
		return fmt.Sprintf("%d %s %d = %d", va.Val, opSym(in.Op), vb.Val, v.Val)
	}
}

func opSym(op Op) string {
	switch op {
	case OpAdd:
		return "+"
	case OpSub:
		return "-"
	case OpMul:
		return "*"
	case OpDiv:
		return "/"
	case OpMod:
		return "%"
	case OpAnd:
		return "&"
	case OpOr:
		return "|"
	case OpXor:
		return "^"
	case OpEq:
		return "=="
	case OpNe:
		return "!="
	case OpLt:
		return "<"
	}
	return "?"
}

// Optimize rewrites the analysed function: folds constant instructions,
// cuts provably dead edges, truncates provably trapping blocks, and
// drops unreachable blocks. Block IDs are renumbered compactly; the
// report always refers to the ORIGINAL block IDs.
func Optimize(a *Analysis) (*Func, *Report) {
	f := a.F
	rep := &Report{}

	idOf := map[int]int{}
	for i, r := range a.Reachable {
		if r {
			idOf[i] = len(idOf)
		}
	}

	nf := &Func{Name: f.Name + "-opt", NumInputs: f.NumInputs, Entry: idOf[f.Entry]}
	for i, blk := range f.Blocks {
		if !a.Reachable[i] {
			rep.Removals = append(rep.Removals, BlockRemoval{
				Block: i,
				Why:   "no feasible path from the entry reaches this block",
			})
			continue
		}
		nb := &Block{ID: idOf[i]}
		trapAt := a.TrapAt[i]
		limit := len(blk.Insts)
		if trapAt >= 0 {
			limit = trapAt + 1 // everything after a proven trap is dead
		}
		for j := 0; j < limit; j++ {
			in := blk.Insts[j]
			v := a.InstVal[i][j]
			if v.Kind == LatConst && in.Op != OpConst && in.Op != OpInput {
				ov := a.OpVal[i][j]
				rep.Folds = append(rep.Folds, ConstFold{
					Block: i, Inst: j, Dst: in.Dst, Value: v.Val,
					Why: foldReason(in, ov[0], ov[1], v),
				})
				nb.Insts = append(nb.Insts, Inst{Dst: in.Dst, Op: OpConst, Imm: v.Val})
				continue
			}
			nb.Insts = append(nb.Insts, in)
		}

		t := blk.Term
		switch {
		case trapAt >= 0:
			nb.Term = Term{Kind: TermTrap}
			for _, e := range outEdges(t) {
				rep.EdgeCuts = append(rep.EdgeCuts, EdgeCut{
					From: i, To: e,
					Why: fmt.Sprintf("block always traps at inst #%d (division by zero), successor is never reached", trapAt),
				})
			}
		case t.Kind == TermBr:
			c := a.OutEnv[i][t.Cond]
			if c.Kind == LatConst {
				taken, dropped := t.ThenBlk, t.ElseBlk
				if c.Val == 0 {
					taken, dropped = t.ElseBlk, t.ThenBlk
				}
				nb.Term = Term{Kind: TermJump, Next: idOf[taken]}
				rep.EdgeCuts = append(rep.EdgeCuts, EdgeCut{
					From: i, To: dropped,
					Why: fmt.Sprintf("branch condition %s is constant %d", t.Cond, c.Val),
				})
			} else {
				// Condition unknown (Over) or undetermined (Undef):
				// keep BOTH edges. An edge we cannot prove dead stays.
				nb.Term = Term{Kind: TermBr, Cond: t.Cond,
					ThenBlk: remap(idOf, t.ThenBlk), ElseBlk: remap(idOf, t.ElseBlk)}
			}
		case t.Kind == TermJump:
			nb.Term = Term{Kind: TermJump, Next: remap(idOf, t.Next)}
		default:
			nb.Term = t
		}
		nf.Blocks = append(nf.Blocks, nb)
	}
	return nf, rep
}

func outEdges(t Term) []int {
	switch t.Kind {
	case TermJump:
		return []int{t.Next}
	case TermBr:
		return []int{t.ThenBlk, t.ElseBlk}
	}
	return nil
}

func remap(idOf map[int]int, id int) int {
	if n, ok := idOf[id]; ok {
		return n
	}
	return id // unreachable target of a kept edge (ill-formed input); keep as-is
}

// CheckExhaustive runs both functions on EVERY possible input vector and
// compares the observable outcomes (return value, trap, divergence).
// It returns the number of input combinations checked.
func CheckExhaustive(orig, opt *Func, stepCap int) (int, error) {
	if orig.NumInputs != opt.NumInputs {
		return 0, fmt.Errorf("input count mismatch: %d vs %d", orig.NumInputs, opt.NumInputs)
	}
	n := orig.NumInputs
	if n > 3 {
		return 0, fmt.Errorf("%d inputs: exhaustive check supports at most 3", n)
	}
	total := 1 << uint(8*n)
	inputs := make([]uint8, n)
	for c := 0; c < total; c++ {
		v := c
		for j := range inputs {
			inputs[j] = uint8(v)
			v >>= 8
		}
		o1 := Run(orig, inputs, stepCap)
		o2 := Run(opt, inputs, stepCap)
		if o1 != o2 {
			return c, fmt.Errorf("mismatch on inputs %v: orig=%+v opt=%+v", inputs, o1, o2)
		}
	}
	return total, nil
}
