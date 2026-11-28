package loopkit

// PureCalls is the default registry of side-effect-free host functions.
// A call not present here is conservatively treated as a side effect:
// it blocks load hoisting and cannot itself be hoisted.
var PureCalls = map[string]bool{}

// MarkPureCall registers a host function name as pure.
func MarkPureCall(name string) { PureCalls[name] = true }

func callIsPure(i *Instr) bool { return PureCalls[i.Callee] }

// LICMReport records what one LICM run did.
type LICMReport struct {
	Hoisted        []*Instr
	BlockedTrap    []*Instr // trapping instrs not dominating all exits
	BlockedAlias   []*Instr // loads blocked by aliasing stores/calls
	BlockedIrred   []*Loop  // irreducible loops left untouched
	SkippedSCCs    []*SCC   // irreducible SCCs never treated as loops
	PreheadersMade int
	Forest         *LoopForest
}

// RunLICM performs natural-loop-only LICM on f and returns a report.
// Irreducible SCCs are never optimized. The function is mutated in
// place; pass a Clone first if the original IR must be retained.
func RunLICM(f *Func) *LICMReport {
	rep := &LICMReport{}
	forest := AnalyzeLoops(f)
	rep.Forest = forest

	for _, l := range forest.Loops {
		if l.Irreducible {
			rep.BlockedIrred = append(rep.BlockedIrred, l)
		}
	}
	rep.SkippedSCCs = forest.IrreducibleSCCs()
	rep.PreheadersMade = forest.EnsurePreheaders()
	oldForest := forest
	forest = AnalyzeLoops(f) // rebuild after CFG mutation
	rep.Forest = forest
	// Rebind preheader blocks onto the freshly rebuilt loop objects by
	// matching headers; block pointers stay stable across analysis.
	for _, nl := range forest.Loops {
		for _, ol := range oldForest.Loops {
			if ol.Header == nl.Header && ol.Preheader != nil {
				nl.Preheader = ol.Preheader
			}
		}
	}

	// Innermost first: hoisting in inner loops then outer loops lets an
	// outer iteration pick up newly invariant definitions.
	ordered := append([]*Loop(nil), forest.Loops...)
	for i, j := 0, len(ordered)-1; i < j; i, j = i+1, j-1 {
		ordered[i], ordered[j] = ordered[j], ordered[i]
	}
	for _, l := range ordered {
		if l.Irreducible {
			continue
		}
		if l.Preheader == nil {
			l.Preheader = preheaderOf(forest, l)
		}
		if l.Preheader == nil {
			continue
		}
		licmLoop(l, rep)
	}
	return rep
}

func preheaderOf(forest *LoopForest, l *Loop) *Block {
	back := map[*Block]bool{}
	for _, p := range predsOf(l.Header) {
		if forest.Dom.Dominates(l.Header, p) {
			back[p] = true
		}
	}
	var outside []*Block
	for _, p := range predsOf(l.Header) {
		if !back[p] {
			outside = append(outside, p)
		}
	}
	if len(outside) == 1 && len(outside[0].Succs()) == 1 &&
		outside[0].Succs()[0] == l.Header {
		return outside[0]
	}
	return nil
}

func licmLoop(l *Loop, rep *LICMReport) {
	inLoop := l.Blocks
	dom := l.Forest.Dom
	invariant := map[*Instr]bool{}
	exits := l.ExitBlocks()
	effects := collectEffects(inLoop)

	definedInLoop := func(i *Instr) bool {
		return i != nil && inLoop[i.Block]
	}
	allOperandsInvariant := func(i *Instr) bool {
		for _, op := range i.Operands {
			if definedInLoop(op) && !invariant[op] {
				return false
			}
		}
		return true
	}
	dominatesAllExits := func(b *Block) bool {
		for _, e := range exits {
			if !dom.Dominates(b, e) {
				return false
			}
		}
		return true
	}

	progress := true
	for progress {
		progress = false
		for _, b := range l.Forest.Dom.RPO() {
			if !inLoop[b] {
				continue
			}
			for _, i := range b.Instrs {
				if invariant[i] || i.Op == OpPhi || i.Op == OpAlloca ||
					i.Op == OpStore {
					continue
				}
				if !allOperandsInvariant(i) {
					continue
				}
				if !hoistSafety(i, b, l, effects, invariant, dominatesAllExits, rep) {
					continue
				}
				hoistToPreheader(i, l.Preheader)
				invariant[i] = true
				rep.Hoisted = append(rep.Hoisted, i)
				progress = true
			}
		}
	}
}

// hoistSafety implements the two-gate policy:
// gate (a) pure non-trapping; gate (b) block dominates every exit.
// Loads and calls get additional memory/alias restrictions.
func hoistSafety(i *Instr, b *Block, l *Loop, effects MemEffects,
	invariant map[*Instr]bool, dominatesAllExits func(*Block) bool,
	rep *LICMReport) bool {

	switch i.Op {
	case OpLoad:
		addr := i.Operands[0]
		if !addrIsLoopInvariant(addr, l.Blocks, invariant) {
			return false
		}
		loc := resolveLoc(addr)
		if len(effects.calls) > 0 || effects.mayAliasStore(loc) {
			rep.BlockedAlias = append(rep.BlockedAlias, i)
			return false
		}
		// A load may itself trap on a bad address; require gate (b).
		return dominatesAllExits(b)

	case OpCall:
		if !callIsPure(i) {
			return false
		}
		return dominatesAllExits(b)

	case OpGEP:
		return addrIsLoopInvariant(i.Operands[0], l.Blocks, invariant)
	}

	if i.Op.isPureCheap() {
		return true // gate (a)
	}
	if i.Op.isTraping() {
		// Gate (b): guard against ghost traps (e.g. conditional x/0).
		if dominatesAllExits(b) {
			return true
		}
		rep.BlockedTrap = append(rep.BlockedTrap, i)
		return false
	}
	return false
}

func hoistToPreheader(i *Instr, ph *Block) {
	src := i.Block
	removeInstr(src, i)
	i.Block = ph
	// Preheader layout: phis first (if any), hoisted code, terminator.
	// Hoisted instructions are never phis, so append after existing code.
	ph.Instrs = append(ph.Instrs, i)
}

func removeInstr(b *Block, i *Instr) {
	for idx, x := range b.Instrs {
		if x == i {
			b.Instrs = append(b.Instrs[:idx], b.Instrs[idx+1:]...)
			return
		}
	}
}
