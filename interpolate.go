package netsec

import "fmt"

// MaxShared is the largest supported number of shared variables. Full
// enumeration of the shared vocabulary costs 2^MaxShared SAT calls.
const MaxShared = 12

// Interpolate computes a Boolean interpolant for the pair (a, b).
//
// When a and b are jointly unsatisfiable it returns an interpolant I such
// that:
//   - I only mentions variables in shared,
//   - a implies I,
//   - I contradicts b (equivalently, b implies not I).
//
// The returned interpolant is the strongest DNF over the shared vocabulary:
// it is the disjunction of every shared minterm under which a remains
// satisfiable. Both required properties are then independently re-verified
// by enumerating all shared assignments with a separate SAT engine; a
// verification failure produces an error instead of an unsound formula.
//
// When a and b are jointly satisfiable no safety proof exists. In that case
// Interpolate returns a Witness satisfying both CNFs and a nil interpolant.
func Interpolate(a, b CNF, shared []int) (*Interpolant, *Witness, error) {
	canonical, err := validateInputs(a, b, shared)
	if err != nil {
		return nil, nil, err
	}

	sat, model := solveCNF(append(append(CNF{}, a...), b...), nil)
	if sat {
		w := &Witness{Assignment: model}
		if !w.Satisfies(a) || !w.Satisfies(b) {
			return nil, nil, ErrInvalidInput
		}
		return nil, w, nil
	}

	it := buildInterpolant(a, canonical)

	report := Verify(a, b, canonical, it)
	if !report.OK() {
		return nil, nil, fmt.Errorf("netsec: interpolant failed independent verification: %s", report.Failure)
	}
	return it, nil, nil
}

// validateInputs enforces the vocabulary limit and structural invariants.
func validateInputs(a, b CNF, shared []int) ([]int, error) {
	if len(shared) > MaxShared {
		return nil, ErrTooManyShared
	}

	seen := make(map[int]struct{}, len(shared))
	for _, v := range shared {
		if v <= 0 {
			return nil, ErrInvalidInput
		}
		if _, dup := seen[v]; dup {
			return nil, ErrInvalidInput
		}
		seen[v] = struct{}{}
	}

	for _, f := range []CNF{a, b} {
		for _, c := range f {
			for _, l := range c {
				if l == 0 || l.Var() <= 0 {
					return nil, ErrInvalidInput
				}
			}
		}
	}

	// A variable occurring on both sides is, by definition, part of the
	// shared vocabulary; otherwise no interpolant over the declared shared
	// set could capture the coupling between the two CNFs.
	inA := make(map[int]struct{})
	for _, v := range a.Vars() {
		inA[v] = struct{}{}
	}
	for _, v := range b.Vars() {
		if _, overlap := inA[v]; overlap {
			if _, declared := seen[v]; !declared {
				return nil, ErrInvalidInput
			}
		}
	}

	out := append([]int(nil), shared...)
	return out, nil
}

// buildInterpolant enumerates all 2^k shared assignments (k <= 12) and keeps
// exactly those under which a remains satisfiable. Because a and b are
// jointly unsatisfiable, any a-feasible shared assignment is necessarily
// b-infeasible, so the kept region simultaneously satisfies A |= I and
// B |= not I. Each kept assignment becomes a full minterm, making the
// disjunction the strongest interpolant expressible in the shared vocabulary.
func buildInterpolant(a CNF, shared []int) *Interpolant {
	total := 1
	for range shared {
		total *= 2
	}

	terms := make([]Term, 0, total)
	for mask := 0; mask < total; mask++ {
		assumptions := maskLiterals(shared, mask)
		feasible, _ := solveCNF(a, assumptions)
		if !feasible {
			continue
		}

		// A full minterm: every shared variable gets a fixed sign. With zero
		// shared variables the single surviving term is empty, i.e. true.
		term := make(Term, 0, len(shared))
		for i, v := range shared {
			l := Literal(v)
			if mask&(1<<uint(i)) == 0 {
				l = -l
			}
			term = append(term, l)
		}
		terms = append(terms, term)
	}

	return &Interpolant{Terms: terms, Shared: append([]int(nil), shared...)}
}
