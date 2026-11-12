package netsec

// guardedOutcome is one terminal path condition and its observable outcome.
type guardedOutcome struct {
	guard   *BNode
	kind    OutcomeKind
	value   bvec // OutcomeReturn
	errNode int  // OutcomeError
	width   int
}

// maxSymbolicPaths bounds path enumeration so branch-heavy graphs cannot
// explode verification time; hitting the cap surfaces as Unknown.
const maxSymbolicPaths = 4096

type pathExplorer struct {
	eng     *engine
	out     []guardedOutcome
	trunc   bool
	pathCnt int
}

// symbolicExecute enumerates acyclic paths and bit-blasts each reachable
// definition once. The returned guards partition the input space (up to
// infeasible constant-false paths).
func symbolicExecute(g *Graph, f *BFactory, prefix string) (outcomes []guardedOutcome, truncated bool, err error) {
	if err = g.Validate(); err != nil {
		return nil, false, err
	}
	pe := &pathExplorer{eng: newEngine(g, f, prefix)}
	pe.walk(g.block(g.Entry), f.Const(true))
	return pe.out, pe.trunc, nil
}

func (pe *pathExplorer) walk(blk *Block, guard *BNode) {
	if guard.op == BConst && !guard.imm {
		return
	}
	pe.pathCnt++
	if pe.pathCnt > maxSymbolicPaths {
		pe.trunc = true
		return
	}
	f := pe.eng.factory
	for _, vid := range blk.Insts {
		pe.eng.blast(vid)
	}
	switch blk.Term {
	case TermReturn:
		rv := pe.eng.g.Values[blk.Ret]
		pe.out = append(pe.out, guardedOutcome{
			guard: guard,
			kind:  OutcomeReturn,
			value: pe.eng.blast(blk.Ret),
			width: rv.Width,
		})
	case TermError:
		pe.out = append(pe.out, guardedOutcome{
			guard:   guard,
			kind:    OutcomeError,
			errNode: blk.ErrNode,
		})
	case TermBranch:
		cond := pe.eng.blast(blk.Cond).bits[0]
		thenGuard := f.And(guard, cond)
		elseGuard := f.And(guard, f.Not(cond))
		pe.walk(pe.eng.g.block(blk.Then), thenGuard)
		pe.walk(pe.eng.g.block(blk.Else), elseGuard)
	}
}
