package netsec

// VerificationReport is the independent verification evidence for an
// interpolant. The checks enumerate every total assignment of the shared
// variables and re-run the SAT solver from scratch for each side, so the
// report does not trust any result produced while constructing the formula.
type VerificationReport struct {
	Passed          bool
	SharedCount     int
	Assignments     int
	AFeasible       int
	BFeasible       int
	ImplicationOK   bool
	ContradictionOK bool
	LanguageOK      bool
	Failure         string
}

// VerifyInterpolant independently checks the two Craig interpolation
// properties for f over shared:
//
//	implication:    every shared assignment feasible for a satisfies f,
//	                hence a |= f;
//	contradiction:  every shared assignment satisfying f is infeasible for b,
//	                hence b & f is unsatisfiable.
//
// It also checks that f only references shared variables and that a & b is
// indeed unsatisfiable. All 2**len(shared) assignments are enumerated.
func VerifyInterpolant(a, b CNF, shared []int, f Formula) (*VerificationReport, error) {
	if err := validateInputs(a, b, shared); err != nil {
		return nil, err
	}
	if f == nil {
		return nil, &InputError{Op: "verification", Msg: "nil interpolant"}
	}
	report := &VerificationReport{
		SharedCount:     len(shared),
		Assignments:     1 << len(shared),
		ImplicationOK:   true,
		ContradictionOK: true,
	}

	// Language check: the interpolant must live entirely in the shared vocab.
	sharedSet := map[int]struct{}{}
	for _, v := range shared {
		sharedSet[v] = struct{}{}
	}
	report.LanguageOK = true
	for _, v := range Vars(f) {
		if _, ok := sharedSet[v]; !ok {
			report.LanguageOK = false
			report.Failure = "interpolant references non-shared variable " + itoa(v)
			break
		}
	}

	// An interpolant is only meaningful when the joint conjunction is UNSAT.
	joint := make(CNF, 0, len(a)+len(b))
	joint = append(joint, a...)
	joint = append(joint, b...)
	if Sat(joint) {
		report.ImplicationOK = false
		report.ContradictionOK = false
		if report.Failure == "" {
			report.Failure = "joint conjunction is satisfiable, no interpolant exists"
		}
		report.Passed = false
		return report, nil
	}

	values := make([]bool, len(shared))
	for {
		env := assignmentEnv(shared, values)

		aFeasible, _ := solveUnder(a, shared, values)
		bFeasible, _ := solveUnder(b, shared, values)
		if aFeasible {
			report.AFeasible++
		}
		if bFeasible {
			report.BFeasible++
		}

		formulaTrue := f.Eval(env)

		// Property 1: a |= f. Any a-feasible shared assignment must satisfy f.
		if aFeasible && !formulaTrue {
			report.ImplicationOK = false
			if report.Failure == "" {
				report.Failure = "implication violated at shared assignment " +
					renderAssignment(shared, values)
			}
		}

		// Property 2: b & f unsat. Any assignment satisfying f must be
		// infeasible for b.
		if formulaTrue && bFeasible {
			report.ContradictionOK = false
			if report.Failure == "" {
				report.Failure = "contradiction violated at shared assignment " +
					renderAssignment(shared, values)
			}
		}

		if !nextAssignment(values) {
			break
		}
	}

	report.Passed = report.LanguageOK && report.ImplicationOK && report.ContradictionOK
	return report, nil
}

// VerifyWitness checks that witness satisfies every clause of cnf. It uses
// direct clause evaluation rather than the solver, giving an independent
// check of the satisfying assignment returned on SAT inputs.
func VerifyWitness(cnf CNF, witness map[int]bool) bool {
	if witness == nil {
		return false
	}
	ok, complete := cnf.Eval(witness)
	return complete && ok
}

func assignmentEnv(shared []int, values []bool) map[int]bool {
	env := make(map[int]bool, len(shared))
	for i, v := range shared {
		env[v] = values[i]
	}
	return env
}

func renderAssignment(shared []int, values []bool) string {
	var b []byte
	b = append(b, '{')
	for i, v := range shared {
		if i > 0 {
			b = append(b, ',', ' ')
		}
		b = append(b, 'x')
		b = append(b, itoa(v)...)
		b = append(b, '=')
		if values[i] {
			b = append(b, '1')
		} else {
			b = append(b, '0')
		}
	}
	b = append(b, '}')
	return string(b)
}
