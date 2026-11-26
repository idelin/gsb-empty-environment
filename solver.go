package seccomp

import "math"

// DefaultSolverBudget caps DPLL search nodes per witness so verification
// stays bounded even for pathological relational constraints.
const DefaultSolverBudget = 200000

type bitRef struct {
	vid int
	bit uint
}

// tupleBitOrder returns the bit branching order that makes the first model
// the lexicographically smallest Tuple: for arg0..arg5 minimize the high
// 32-bit word then the low word, each from MSB to LSB; instruction pointer
// follows; remaining words trail.
func tupleBitOrder(numVars int) []bitRef {
	var order []bitRef
	emit := func(vid int) {
		if vid < 0 || vid >= numVars {
			return
		}
		for b := 31; b >= 0; b-- {
			order = append(order, bitRef{vid: vid, bit: uint(b)})
		}
	}
	for i := 0; i < ArgCount; i++ {
		emit(ArgHiWord(i))
		emit(ArgLoWord(i))
	}
	emit(WordIPHi)
	emit(WordIPLo)
	for vid := 0; vid < numVars; vid++ {
		if vid == WordNR || vid == WordArch ||
			vid == WordIPLo || vid == WordIPHi || isArgWord(vid) {
			continue
		}
		emit(vid)
	}
	emit(WordNR)
	emit(WordArch)
	return order
}

func isArgWord(vid int) bool {
	return vid >= WordArg0Lo && vid < WordArg0Lo+2*ArgCount
}

type solver struct {
	preds  []Pred
	pvars  [][]int
	order  []bitRef
	budget int
	nodes  int
	abort  bool
}

// solveMinimal finds the smallest concrete word assignment satisfying preds
// and the seed domains.  It returns budgetExceeded=true if the node cap was
// hit (the absence of a model is then not proven).
func solveMinimal(preds []Pred, seeds []Dom, budget int) (vals []uint32, complete bool, budgetExceeded bool) {
	if budget <= 0 {
		budget = DefaultSolverBudget
	}
	pv := make([][]int, len(preds))
	for i, p := range preds {
		m := p.vars()
		ids := make([]int, 0, len(m))
		for id := range m {
			ids = append(ids, id)
		}
		pv[i] = ids
	}
	s := &solver{
		preds:  preds,
		pvars:  pv,
		order:  tupleBitOrder(len(seeds)),
		budget: budget,
	}
	sol, ok := s.dpll(append([]Dom(nil), seeds...))
	if s.abort {
		return nil, false, true
	}
	if !ok {
		return nil, true, false
	}
	return sol, true, false
}

func (s *solver) feasible(dv []Dom) bool {
	vals := make([]uint32, len(dv))
	for i := range dv {
		if c, ok := dv[i].isConst(); ok {
			vals[i] = c
		}
	}
	for pi, p := range s.preds {
		allConst := true
		for _, id := range s.pvars[pi] {
			if id < 0 || id >= len(dv) || dv[id].km != math.MaxUint32 {
				allConst = false
				break
			}
		}
		if allConst {
			ok, concrete := checkPredExact(p, vals)
			if concrete && !ok {
				return false
			}
			if !concrete {
				// Division by zero on this assignment.
				return false
			}
		} else if !feasiblePredAbstract(p, dv) {
			return false
		}
	}
	return true
}

func withBit(dv []Dom, vid int, mask uint32, one bool) []Dom {
	next := append([]Dom(nil), dv...)
	d := next[vid]
	d.km |= mask
	if one {
		d.on |= mask
	} else {
		d.on &^= mask
	}
	next[vid] = d
	return next
}

func (s *solver) dpll(dv []Dom) ([]uint32, bool) {
	s.nodes++
	if s.nodes > s.budget {
		s.abort = true
		return nil, false
	}
	if !s.feasible(dv) {
		return nil, false
	}
	for _, br := range s.order {
		if br.vid >= len(dv) {
			continue
		}
		mask := uint32(1) << br.bit
		if dv[br.vid].km&mask == 0 {
			if sol, ok := s.dpll(withBit(dv, br.vid, mask, false)); ok {
				return sol, true
			}
			if s.abort {
				return nil, false
			}
			if sol, ok := s.dpll(withBit(dv, br.vid, mask, true)); ok {
				return sol, true
			}
			return nil, false
		}
	}
	vals := make([]uint32, len(dv))
	for i, d := range dv {
		c, ok := d.isConst()
		if !ok {
			return nil, false
		}
		vals[i] = c
	}
	return vals, true
}
