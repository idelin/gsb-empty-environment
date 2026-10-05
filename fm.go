package main

import (
	"fmt"
	"math/big"
)

// BudgetExceededError is returned when the number of pairwise combinations
// generated during elimination exceeds the configured budget. The caller
// must not present any partial result as equivalent to the input system.
type BudgetExceededError struct {
	Generated int
	Budget    int
}

func (e *BudgetExceededError) Error() string {
	return fmt.Sprintf("elimination budget exceeded: %d combinations generated, budget is %d", e.Generated, e.Budget)
}

type RemovedIneq struct {
	Ineq   *Ineq
	Reason string
}

type ElimResult struct {
	Kept          []*Ineq
	Removed       []RemovedIneq
	Contradiction *Ineq
}

// eliminateAll projects out the given variable indices in order.
func eliminateAll(ineqs []*Ineq, elim []int, budget int) (*ElimResult, error) {
	res := &ElimResult{}
	current := ineqs
	generated := 0
	for _, v := range elim {
		next, err := fmStep(current, v, budget, &generated)
		if err != nil {
			return nil, err
		}
		kept, removed, contra := reduce(next)
		res.Removed = append(res.Removed, removed...)
		if contra != nil {
			res.Contradiction = contra
			return res, nil
		}
		current = kept
	}
	kept, removed, contra := reduce(current)
	res.Removed = append(res.Removed, removed...)
	res.Kept = kept
	res.Contradiction = contra
	return res, nil
}

func fmStep(ineqs []*Ineq, v, budget int, generated *int) ([]*Ineq, error) {
	var lower, upper, rest []*Ineq
	for _, in := range ineqs {
		s := in.Coeffs[v].Sign()
		switch {
		case s < 0:
			lower = append(lower, in)
		case s > 0:
			upper = append(upper, in)
		default:
			rest = append(rest, in)
		}
	}
	out := rest
	for _, l := range lower {
		for _, u := range upper {
			*generated++
			if *generated > budget {
				return nil, &BudgetExceededError{Generated: *generated, Budget: budget}
			}
			out = append(out, combine(l, u, v))
		}
	}
	return out, nil
}

// combine eliminates variable v from a lower bound l (coefficient < 0) and
// an upper bound u (coefficient > 0) using positive multipliers. The result
// is strict iff either parent is strict.
func combine(l, u *Ineq, v int) *Ineq {
	ml := new(big.Rat).Set(u.Coeffs[v])
	mu := new(big.Rat).Neg(l.Coeffs[v])
	n := len(l.Coeffs)
	coeffs := make([]*big.Rat, n)
	for i := 0; i < n; i++ {
		coeffs[i] = new(big.Rat).Add(
			new(big.Rat).Mul(ml, l.Coeffs[i]),
			new(big.Rat).Mul(mu, u.Coeffs[i]),
		)
	}
	rhs := new(big.Rat).Add(new(big.Rat).Mul(ml, l.RHS), new(big.Rat).Mul(mu, u.RHS))
	return &Ineq{
		Coeffs: coeffs,
		RHS:    rhs,
		Strict: l.Strict || u.Strict,
		Prov:   mergeProv(l.Prov, ml, u.Prov, mu),
	}
}

const maxDerivations = 16

func mergeProv(pl [][]ProvTerm, ml *big.Rat, pu [][]ProvTerm, mu *big.Rat) [][]ProvTerm {
	var out [][]ProvTerm
	for _, dl := range pl {
		for _, du := range pu {
			if len(out) >= maxDerivations {
				return out
			}
			out = append(out, mergeDerivation(scaleDerivation(dl, ml), scaleDerivation(du, mu)))
		}
	}
	return out
}

func scaleDerivation(d []ProvTerm, f *big.Rat) []ProvTerm {
	out := make([]ProvTerm, len(d))
	for i, t := range d {
		out[i] = ProvTerm{ID: t.ID, Mult: new(big.Rat).Mul(t.Mult, f)}
	}
	return out
}

func mergeDerivation(a, b []ProvTerm) []ProvTerm {
	out := make([]ProvTerm, len(a))
	for i, t := range a {
		out[i] = ProvTerm{ID: t.ID, Mult: new(big.Rat).Set(t.Mult)}
	}
	for _, t := range b {
		found := false
		for i := range out {
			if out[i].ID == t.ID {
				out[i].Mult.Add(out[i].Mult, t.Mult)
				found = true
				break
			}
		}
		if !found {
			out = append(out, ProvTerm{ID: t.ID, Mult: new(big.Rat).Set(t.Mult)})
		}
	}
	return out
}

// reduce drops tautologies, exact duplicates and dominated constraints.
// Every dropped constraint is returned with its provenance and the reason.
// A zero-coefficient constraint that is false is returned as a contradiction.
func reduce(ineqs []*Ineq) (kept []*Ineq, removed []RemovedIneq, contra *Ineq) {
	var rest []*Ineq
	for _, in := range ineqs {
		if in.isZero() {
			if in.triviallyTrue() {
				removed = append(removed, RemovedIneq{in, "tautology: constant constraint is always true"})
			} else {
				contra = in
			}
		} else {
			rest = append(rest, in)
		}
	}
	if contra != nil {
		return nil, removed, contra
	}
	byKey := map[string]*Ineq{}
	var order []string
	for _, in := range rest {
		k := canonicalKey(in)
		if prev, ok := byKey[k]; ok {
			prev.Prov = append(prev.Prov, in.Prov...)
			removed = append(removed, RemovedIneq{in, "duplicate of kept constraint " + prev.String()})
		} else {
			byKey[k] = in
			order = append(order, k)
		}
	}
	var groupKeys []string
	groups := map[string][]*Ineq{}
	for _, k := range order {
		in := byKey[k]
		ck := coeffKey(in.Coeffs)
		if _, ok := groups[ck]; !ok {
			groupKeys = append(groupKeys, ck)
		}
		groups[ck] = append(groups[ck], in)
	}
	for _, ck := range groupKeys {
		group := groups[ck]
		strongest := group[0]
		for _, in := range group[1:] {
			if dominates(in, strongest) {
				strongest = in
			}
		}
		for _, in := range group {
			if in != strongest {
				removed = append(removed, RemovedIneq{in, "dominated by kept constraint " + strongest.String()})
			}
		}
		kept = append(kept, strongest)
	}
	return kept, removed, nil
}

// dominates reports whether a implies b, assuming identical coefficient
// direction: a has the smaller right-hand side, or equal rhs but stricter.
func dominates(a, b *Ineq) bool {
	c := a.RHS.Cmp(b.RHS)
	if c < 0 {
		return true
	}
	if c > 0 {
		return false
	}
	return a.Strict && !b.Strict
}

// lpReduce removes constraints that are implied by the remaining ones,
// using the exact LP solver. Strict constraints of others are relaxed to
// non-strict, which keeps the test sound. Evidence (the achieved maximum
// of the left-hand side) is recorded for every removed constraint.
func lpReduce(kept []*Ineq, maxLPs int) ([]*Ineq, []RemovedIneq) {
	var removed []RemovedIneq
	current := kept
	lps := 0
	i := 0
	for i < len(current) && lps < maxLPs {
		cand := current[i]
		others := make([]*Ineq, 0, len(current)-1)
		others = append(others, current[:i]...)
		others = append(others, current[i+1:]...)
		var A [][]*big.Rat
		var b []*big.Rat
		for _, o := range others {
			A = append(A, o.Coeffs)
			b = append(b, o.RHS)
		}
		res := lpMaximizeFree(A, b, cand.Coeffs)
		lps++
		redundant := false
		if res.Status == LPOptimal {
			if cand.Strict {
				redundant = res.Value.Cmp(cand.RHS) < 0
			} else {
				redundant = res.Value.Cmp(cand.RHS) <= 0
			}
		}
		if redundant {
			reason := fmt.Sprintf("lp-implied by remaining constraints (max lhs = %s)", res.Value.RatString())
			removed = append(removed, RemovedIneq{cand, reason})
			current = others
		} else {
			i++
		}
	}
	return current, removed
}
