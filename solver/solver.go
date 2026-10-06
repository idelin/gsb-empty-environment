// Package solver decides satisfiability of finite-domain constraint sets
// by exhaustive enumeration and computes inclusion-minimal unsatisfiable
// subsets (MUS) with a deterministic, input-order deletion pass.
package solver

import (
	"musdiag/csp"
)

// Result is the outcome of a satisfiability check.
type Result int

const (
	// UNSAT means every complete assignment was enumerated and none
	// satisfies all constraints.
	UNSAT Result = iota
	// SAT means a satisfying assignment was found.
	SAT
	// Unknown means the assignment space exceeds the enumeration limit.
	Unknown
)

func (r Result) String() string {
	switch r {
	case UNSAT:
		return "UNSAT"
	case SAT:
		return "SAT"
	}
	return "UNKNOWN"
}

// DefaultLimit caps the number of complete assignments examined.
const DefaultLimit int64 = 100000000

// Check decides whether cons is satisfiable over p's variable domains by
// exhaustive enumeration. Variables are assigned in declaration order and
// constraints are tested as soon as all their variables are bound, which
// prunes the search without skipping any complete assignment. On SAT it
// returns the first satisfying assignment in lexicographic order of that
// enumeration, making results fully deterministic.
func Check(p *csp.Problem, cons []csp.Constraint, limit int64) (map[string]int, Result) {
	if p.Combinations(limit) > limit {
		return nil, Unknown
	}
	vars := p.Vars
	// ready[i] lists constraints checkable once vars[0..i] are assigned.
	ready := make([][]csp.Constraint, len(vars))
	for _, c := range cons {
		depth := -1
		for _, name := range c.Vars() {
			for i, v := range vars {
				if v.Name == name && i > depth {
					depth = i
				}
			}
		}
		if depth < 0 { // constraint over no variables: decide immediately
			if !c.Holds(nil) {
				return nil, UNSAT
			}
			continue
		}
		ready[depth] = append(ready[depth], c)
	}
	env := make(map[string]int, len(vars))
	var walk func(i int) bool
	walk = func(i int) bool {
		if i == len(vars) {
			return true
		}
		v := vars[i]
		for val := v.Lo; val <= v.Hi; val++ {
			env[v.Name] = val
			ok := true
			for _, c := range ready[i] {
				if !c.Holds(env) {
					ok = false
					break
				}
			}
			if ok && walk(i+1) {
				return true
			}
		}
		delete(env, v.Name)
		return false
	}
	if walk(0) {
		return env, SAT
	}
	return nil, UNSAT
}

// FindMUS reduces p.Cons to an inclusion-minimal unsatisfiable subset:
// the returned set is unsatisfiable, but removing any single constraint
// from it makes it satisfiable. It is not necessarily cardinality-minimal.
//
// The reduction is a deterministic deletion pass in input order: each
// constraint, in the order given, is tentatively dropped and kept out
// exactly when the remainder is still unsatisfiable. Because the pass
// order and the enumeration order are fixed, the result is a pure
// function of the input file.
//
// If the whole set is satisfiable, FindMUS returns (nil, witness, SAT).
// If any check exceeds the enumeration limit it returns res == Unknown.
func FindMUS(p *csp.Problem, limit int64) (mus []csp.Constraint, witness map[string]int, res Result) {
	witness, res = Check(p, p.Cons, limit)
	if res != UNSAT {
		return nil, witness, res
	}
	cur := make([]csp.Constraint, len(p.Cons))
	copy(cur, p.Cons)
	for _, c := range p.Cons { // input order
		cand := make([]csp.Constraint, 0, len(cur)-1)
		for _, k := range cur {
			if k.Name != c.Name {
				cand = append(cand, k)
			}
		}
		if len(cand) == len(cur) {
			continue // already removed
		}
		_, r := Check(p, cand, limit)
		if r == Unknown {
			return nil, nil, Unknown
		}
		if r == UNSAT {
			cur = cand
		}
	}
	return cur, nil, UNSAT
}

// VerifyMUS independently re-proves that mus is an inclusion-minimal
// unsatisfiable subset: the full set must be UNSAT and every
// single-constraint deletion must be SAT. It returns one witness per
// deleted constraint, in mus order. ok is false if any check fails or
// hits the enumeration limit.
func VerifyMUS(p *csp.Problem, mus []csp.Constraint, limit int64) (witnesses map[string]map[string]int, ok bool) {
	if _, r := Check(p, mus, limit); r != UNSAT {
		return nil, false
	}
	witnesses = map[string]map[string]int{}
	for _, drop := range mus {
		sub := make([]csp.Constraint, 0, len(mus)-1)
		for _, c := range mus {
			if c.Name != drop.Name {
				sub = append(sub, c)
			}
		}
		env, r := Check(p, sub, limit)
		if r != SAT {
			return nil, false
		}
		witnesses[drop.Name] = env
	}
	return witnesses, true
}
