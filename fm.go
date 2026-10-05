package main

import (
	"fmt"
	"math/big"
)

// Status is the top-level outcome of an elimination run.
type Status string

const (
	StatusOK             Status = "ok"
	StatusBudgetExceeded Status = "budget_exceeded"
)

// Budget bounds the work the eliminator may do. When a budget is exhausted
// the engine stops and returns the exact (but not fully projected) system
// with StatusBudgetExceeded; it never drops generated constraints to fake a
// smaller "equivalent" result.
type Budget struct {
	MaxConstraints int   // max constraints allowed in the working set after a round
	MaxOps         int64 // max combination operations (pairings + substitutions)
}

// Removed records a constraint deleted from the output together with the
// combination evidence (its source set) and the reason for deletion.
type Removed struct {
	C      *Constraint
	Reason string // "tautology" | "duplicate" | "implied_by_others"
}

// ElimResult is the outcome of Eliminate.
type ElimResult struct {
	Status       Status
	Feasible     bool          // false iff a contradiction was derived
	Constraints  []*Constraint // exact system; fully projected only when Status==StatusOK
	Removed      []Removed
	Uneliminated []int // variables still present when the budget ran out
	Ops          int64
	Rounds       int
}

// Engine runs Fourier-Motzkin elimination with exact rational arithmetic.
type Engine struct {
	NumVars int
	Budget  Budget
	Prune   bool // run the LP-based redundancy pass on the final system
	ops     int64
}

// Eliminate projects sys onto the variables not listed in elim.
func (e *Engine) Eliminate(sys []*Constraint, elim []int) *ElimResult {
	e.ops = 0
	res := &ElimResult{Status: StatusOK, Feasible: true}
	current := make([]*Constraint, len(sys))
	for i, c := range sys {
		current[i] = c.Clone()
	}
	elimSet := map[int]bool{}
	for _, v := range elim {
		elimSet[v] = true
	}

	var feasible bool
	current, feasible = e.tidy(current, res)
	if !feasible {
		res.Feasible = false
		res.Constraints = current
		return res
	}

	for {
		v := e.pickVar(current, elimSet)
		if v < 0 {
			break // nothing left to eliminate
		}
		elimSet[v] = false
		if eq := findEquality(current, v); eq >= 0 {
			current = e.substitute(current, eq, v)
		} else {
			next, ok := e.fmRound(current, v)
			if !ok {
				// budget would be exceeded: stop here with the exact
				// intermediate system; v and possibly others remain.
				elimSet[v] = true
				res.Status = StatusBudgetExceeded
				break
			}
			current = next
		}
		res.Rounds++
		current, feasible = e.tidy(current, res)
		if !feasible {
			res.Feasible = false
			res.Constraints = current
			return res
		}
		if len(current) > e.Budget.MaxConstraints || e.ops > e.Budget.MaxOps {
			res.Status = StatusBudgetExceeded
			break
		}
	}

	res.Ops = e.ops
	for v := range elimSet {
		if appearsIn(current, v) {
			res.Uneliminated = append(res.Uneliminated, v)
		}
	}
	if res.Status == StatusOK && e.Prune {
		current = e.pruneRedundant(current, res)
	}
	res.Constraints = current
	return res
}

// pickVar chooses the next variable to eliminate (greedy: prefer equality
// substitution, then minimum fill-in).
func (e *Engine) pickVar(sys []*Constraint, elimSet map[int]bool) int {
	best := -1
	bestCost := int64(-1)
	for v := range elimSet {
		if !appearsIn(sys, v) {
			delete(elimSet, v)
			continue
		}
		var cost int64
		if findEquality(sys, v) >= 0 {
			cost = 0
		} else {
			var l, u int64
			for _, c := range sys {
				s := c.Coeff[v].Sign()
				if s < 0 {
					l++
				} else if s > 0 {
					u++
				}
			}
			cost = l * u
		}
		if best < 0 || cost < bestCost {
			best = v
			bestCost = cost
		}
	}
	return best
}

func appearsIn(sys []*Constraint, v int) bool {
	for _, c := range sys {
		if c.Coeff[v].Sign() != 0 {
			return true
		}
	}
	return false
}

func findEquality(sys []*Constraint, v int) int {
	for i, c := range sys {
		if c.Kind == Eq && c.Coeff[v].Sign() != 0 {
			return i
		}
	}
	return -1
}

// substitute solves eq for v and replaces v everywhere else.
func (e *Engine) substitute(sys []*Constraint, eqIdx, v int) []*Constraint {
	eq := sys[eqIdx]
	cv := eq.Coeff[v]
	out := make([]*Constraint, 0, len(sys)-1)
	for i, c := range sys {
		if i == eqIdx {
			continue
		}
		if c.Coeff[v].Sign() == 0 {
			out = append(out, c)
			continue
		}
		// c' = c - (c_v / eq_v) * eq
		s := new(big.Rat).Quo(c.Coeff[v], cv)
		s.Neg(s)
		out = append(out, addScaled(c, eq, s))
		e.ops++
	}
	return out
}

// fmRound performs one Fourier-Motzkin pairing round on v. Returns ok=false
// (without modifying anything) if the round would exceed the budget.
func (e *Engine) fmRound(sys []*Constraint, v int) ([]*Constraint, bool) {
	var lower, upper, rest []*Constraint
	for _, c := range sys {
		s := c.Coeff[v].Sign()
		switch {
		case s < 0:
			lower = append(lower, c)
		case s > 0:
			upper = append(upper, c)
		default:
			rest = append(rest, c)
		}
	}
	pairs := int64(len(lower)) * int64(len(upper))
	if len(rest)+int(pairs) > e.Budget.MaxConstraints || e.ops+pairs > e.Budget.MaxOps {
		return nil, false
	}
	out := append([]*Constraint(nil), rest...)
	for _, l := range lower {
		for _, u := range upper {
			// multipliers are positive: cu*l + (-cl)*u eliminates v
			cu := u.Coeff[v]
			clNeg := new(big.Rat).Neg(l.Coeff[v])
			comb := combine(l, u, cu, clNeg)
			comb.normalize()
			out = append(out, comb)
			e.ops++
		}
	}
	return out, true
}

// tidy normalizes, drops tautologies and merges duplicates (unioning their
// source evidence). Reports false when a contradiction is derived; the
// returned system then holds the contradictory witness constraint.
func (e *Engine) tidy(sys []*Constraint, res *ElimResult) ([]*Constraint, bool) {
	out := []*Constraint{}
	seen := map[string]int{}
	for _, c := range sys {
		if c.isZeroRow() {
			if c.tautology() {
				res.Removed = append(res.Removed, Removed{C: c, Reason: "tautology"})
				continue
			}
			// contradiction: system is infeasible; keep the witness
			return []*Constraint{c}, false
		}
		c.normalize()
		k := c.key()
		if idx, dup := seen[k]; dup {
			out[idx].SetSource(unionSources(out[idx].Source, c.Source))
			res.Removed = append(res.Removed, Removed{C: c, Reason: "duplicate"})
			continue
		}
		seen[k] = len(out)
		out = append(out, c)
	}
	return out, true
}

// pruneRedundant deletes constraints implied by the rest of the system,
// keeping each deleted constraint (with its source evidence) in res.Removed.
// Each deletion is justified by an exact feasibility check of the negation,
// so equivalence is preserved at every step.
func (e *Engine) pruneRedundant(sys []*Constraint, res *ElimResult) []*Constraint {
	const pruneLimit = 300
	if len(sys) > pruneLimit {
		res.Removed = append(res.Removed, Removed{
			C:      nil,
			Reason: fmt.Sprintf("redundancy_check_skipped: %d constraints exceed limit %d", len(sys), pruneLimit),
		})
		return sys
	}
	cur := append([]*Constraint(nil), sys...)
	i := 0
	for i < len(cur) {
		c := cur[i]
		if c.Kind == Eq {
			i++
			continue
		}
		others := make([]*Constraint, 0, len(cur))
		others = append(others, cur[:i]...)
		others = append(others, cur[i+1:]...)
		test := append(others, c.negate())
		if !FeasibleMixed(test, e.NumVars) {
			res.Removed = append(res.Removed, Removed{C: c, Reason: "implied_by_others"})
			cur = others
			continue // re-examine index i
		}
		i++
	}
	return cur
}
