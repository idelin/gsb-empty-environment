package netsec

// Result is the outcome of an interpolation request.
//
// When SAT is true, A and B are jointly satisfiable and Witness is a concrete
// satisfying assignment: no interpolant exists and Interpolant is nil. The
// package never fabricates a safety proof in this case.
//
// When SAT is false, Interpolant is a formula over Shared such that:
//
//	A |= Interpolant        (the antecedent implies the interpolant)
//	B  & Interpolant unsat  (the interpolant contradicts the consequent)
//
// Verification holds the independent enumeration report that re-checked both
// properties without reusing any data collected during construction.
type Result struct {
	SAT          bool
	Witness      map[int]bool
	Interpolant  Formula
	Shared       []int
	Enumerations int
	Verification *VerificationReport
}

// Interpolate computes a finite Boolean Craig-style interpolant for the CNF
// pair (a, b) over the shared variables.
//
// The shared set must contain every variable occurring in both a and b and
// must hold at most MaxSharedVars variables. If a & b is satisfiable the
// satisfying witness is returned instead of an interpolant.
func Interpolate(a, b CNF, shared []int) (*Result, error) {
	if err := validateInputs(a, b, shared); err != nil {
		return nil, err
	}

	joint := make(CNF, 0, len(a)+len(b))
	joint = append(joint, a...)
	joint = append(joint, b...)

	if ok, witness := Solve(joint); ok {
		if !VerifyWitness(joint, witness) {
			return nil, &InputError{Op: "solver", Msg: "internal error: witness does not satisfy input"}
		}
		return &Result{
			SAT:     true,
			Witness: witness,
			Shared:  append([]int(nil), shared...),
		}, nil
	}

	terms, enumerations := buildInterpolantTerms(a, shared)

	var interpolant Formula
	switch {
	case len(terms) == 0:
		// A is unsatisfiable by itself; false is the strongest interpolant.
		interpolant = Const{V: false}
	case len(terms) == 1<<len(shared):
		// Every shared assignment extends to a model of A; true still
		// contradicts B because the joint conjunction is unsatisfiable.
		interpolant = Const{V: true}
	default:
		interpolant = Or{Fs: terms}
	}

	report, err := VerifyInterpolant(a, b, shared, interpolant)
	if err != nil {
		return nil, err
	}
	if !report.Passed {
		return nil, &InputError{Op: "verification", Msg: report.Failure}
	}

	return &Result{
		SAT:          false,
		Interpolant:  interpolant,
		Shared:       append([]int(nil), shared...),
		Enumerations: enumerations,
		Verification: report,
	}, nil
}

// buildInterpolantTerms enumerates every total assignment of the shared
// variables and keeps exactly those that can be extended to a model of a.
// Each kept assignment becomes one complete conjunction term. The result is
// the disjunction that is true precisely on A-feasible shared assignments.
func buildInterpolantTerms(a CNF, shared []int) ([]Formula, int) {
	n := len(shared)
	values := make([]bool, n)
	var terms []Formula
	enumerations := 0
	for {
		enumerations++
		if feasible, _ := solveUnder(a, shared, values); feasible {
			terms = append(terms, conjunction(shared, values))
		}
		if !nextAssignment(values) {
			break
		}
	}
	return terms, enumerations
}

// nextAssignment increments the Boolean counter in place, least significant
// position first. It reports whether another assignment exists.
func nextAssignment(values []bool) bool {
	for i := range values {
		if !values[i] {
			values[i] = true
			for j := 0; j < i; j++ {
				values[j] = false
			}
			return true
		}
	}
	return false
}

func validateInputs(a, b CNF, shared []int) error {
	if err := validateCNF(a); err != nil {
		return err
	}
	if err := validateCNF(b); err != nil {
		return err
	}
	if len(shared) > MaxSharedVars {
		return &InputError{
			Op:  "shared",
			Msg: "too many shared variables (max " + itoa(MaxSharedVars) + ")",
		}
	}
	seen := map[int]struct{}{}
	for _, v := range shared {
		if v < 0 {
			return &InputError{Op: "shared", Msg: "negative variable index"}
		}
		if v > MaxVarIndex {
			return &InputError{Op: "shared", Msg: "variable index exceeds MaxVarIndex"}
		}
		if _, dup := seen[v]; dup {
			return &InputError{Op: "shared", Msg: "duplicate shared variable"}
		}
		seen[v] = struct{}{}
	}
	// Every variable appearing on both sides must be declared shared,
	// otherwise an interpolant over the declared set cannot exist.
	aVars := map[int]struct{}{}
	for _, v := range a.Vars() {
		aVars[v] = struct{}{}
	}
	for _, v := range b.Vars() {
		if _, inA := aVars[v]; inA {
			if _, declared := seen[v]; !declared {
				return &InputError{
					Op:  "shared",
					Msg: "variable " + itoa(v) + " occurs in both CNFs but is not shared",
				}
			}
		}
	}
	return nil
}
