package netsec

import "fmt"

// VerificationReport summarizes the independent enumeration check of an
// interpolant. Every flag must be true for the result to count as a proof.
type VerificationReport struct {
	// PreconditionOK means A and B were independently re-confirmed jointly
	// unsatisfiable.
	PreconditionOK bool

	// VocabularyOK means every literal of the interpolant names a shared
	// variable; no private variable may leak into the proof.
	VocabularyOK bool

	// ImpliedByAOK means A |= I, verified as A and not I being unsatisfiable
	// on every shared-variable assignment.
	ImpliedByAOK bool

	// ContradictsBOK means B |= not I, verified as B and I being
	// unsatisfiable on every shared-variable assignment.
	ContradictsBOK bool

	// StrongestFormOK means the interpolant is exactly the strongest DNF
	// over the shared vocabulary produced by full enumeration.
	StrongestFormOK bool

	// AssignmentCount is the number of shared assignments enumerated.
	AssignmentCount int

	// Failure is empty on success and otherwise names the violated check.
	Failure string
}

// OK reports whether all soundness checks passed.
func (r *VerificationReport) OK() bool {
	return r.PreconditionOK && r.VocabularyOK && r.ImpliedByAOK &&
		r.ContradictsBOK && r.StrongestFormOK && r.Failure == ""
}

// Verify independently re-checks an interpolant against the two input CNFs by
// enumerating all assignments of the shared vocabulary (at most 2^12 = 4096).
// It uses a SAT engine separate from the construction-time solver and never
// trusts any previously computed answer.
func Verify(a, b CNF, shared []int, it *Interpolant) *VerificationReport {
	r := &VerificationReport{}

	if it == nil {
		r.Failure = "nil interpolant"
		return r
	}

	if sat, _ := indSolve(append(append(CNF{}, a...), b...), nil); sat {
		r.Failure = "precondition violated: A and B are jointly satisfiable"
		return r
	}
	r.PreconditionOK = true

	sharedSet := make(map[int]struct{}, len(shared))
	for _, v := range shared {
		sharedSet[v] = struct{}{}
	}
	for _, t := range it.Terms {
		for _, l := range t {
			if _, ok := sharedSet[l.Var()]; !ok {
				r.Failure = fmt.Sprintf("interpolant uses non-shared variable %d", l.Var())
				return r
			}
		}
	}
	r.VocabularyOK = true

	r.ImpliedByAOK = true
	r.ContradictsBOK = true
	r.StrongestFormOK = true

	total := 1
	for range shared {
		total *= 2
	}

	for mask := 0; mask < total; mask++ {
		r.AssignmentCount++
		assign := maskAssignment(shared, mask)
		assumptions := maskLiterals(shared, mask)

		aSat, _ := indSolve(a, assumptions)
		bSat, _ := indSolve(b, assumptions)
		holds := it.Eval(assign)

		// A |= I: whenever A is feasible, I must hold.
		if aSat && !holds {
			r.ImpliedByAOK = false
			r.Failure = fmt.Sprintf("A does not imply interpolant under %v", assign)
			return r
		}

		// B |= not I: whenever B is feasible, I must be false.
		if bSat && holds {
			r.ContradictsBOK = false
			r.Failure = fmt.Sprintf("interpolant does not contradict B under %v", assign)
			return r
		}

		// Strongest DNF: I holds exactly on the feasible assignments of A.
		// Joint unsatisfiability guarantees those assignments are infeasible
		// for B, which the contradict-B check above independently confirms.
		if holds != aSat {
			r.StrongestFormOK = false
			r.Failure = fmt.Sprintf("interpolant is not the strongest shared formula under %v", assign)
			return r
		}
	}

	return r
}

// indSolve is the verification-side SAT engine. It is implemented in a
// deliberately different style from solveCNF: assignments live in immutable
// maps that are copied at every branch, propagation rescans clauses, and no
// state from the construction solver is ever consulted. Sharing a bug between
// both engines is therefore structurally unlikely.
func indSolve(f CNF, assumptions []Literal) (bool, map[int]bool) {
	assign := make(map[int]bool)
	for _, l := range assumptions {
		v, want := l.Var(), l.IsPositive()
		if got, ok := assign[v]; ok && got != want {
			return false, nil
		}
		assign[v] = want
	}
	return indSearch(f, assign)
}

func indSearch(f CNF, assign map[int]bool) (bool, map[int]bool) {
	propagated, conflict := indPropagate(f, assign)
	if conflict {
		return false, nil
	}
	assign = propagated

	v := indPickVar(f, assign)
	if v == 0 {
		return true, assign
	}

	for _, want := range []bool{true, false} {
		next := make(map[int]bool, len(assign)+1)
		for k, val := range assign {
			next[k] = val
		}
		next[v] = want
		if sat, model := indSearch(f, next); sat {
			return true, model
		}
	}
	return false, nil
}

// indPropagate runs unit propagation by repeatedly scanning every clause
// until a fixpoint. It returns the extended assignment, or conflict = true.
func indPropagate(f CNF, assign map[int]bool) (map[int]bool, bool) {
	for {
		progressed := false

		for _, c := range f {
			open := 0
			var unit Literal
			satisfied := false

			for _, l := range c {
				val, ok := assign[l.Var()]
				if !ok {
					open++
					unit = l
					continue
				}
				if val == l.IsPositive() {
					satisfied = true
					break
				}
			}

			if satisfied {
				continue
			}
			if open == 0 {
				return nil, true
			}
			if open == 1 {
				v, want := unit.Var(), unit.IsPositive()
				got, exists := assign[v]
				if exists && got != want {
					return nil, true
				}
				if !exists {
					assign[v] = want
					progressed = true
				}
			}
		}

		if !progressed {
			return assign, false
		}
	}
}

func indPickVar(f CNF, assign map[int]bool) int {
	for _, c := range f {
		for _, l := range c {
			if _, ok := assign[l.Var()]; !ok {
				return l.Var()
			}
		}
	}
	return 0
}

// maskAssignment decodes an enumeration mask into a shared-variable assignment.
func maskAssignment(shared []int, mask int) map[int]bool {
	out := make(map[int]bool, len(shared))
	for i, v := range shared {
		out[v] = mask&(1<<uint(i)) != 0
	}
	return out
}

// maskLiterals returns the assumption literals encoding the same mask.
func maskLiterals(shared []int, mask int) []Literal {
	out := make([]Literal, 0, len(shared))
	for i, v := range shared {
		l := Literal(v)
		if mask&(1<<uint(i)) == 0 {
			l = -l
		}
		out = append(out, l)
	}
	return out
}
