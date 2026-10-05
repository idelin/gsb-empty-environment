package main

import "fmt"

type Status int

const (
	Killed Status = iota
	Survived
	Equivalent
)

func (s Status) String() string {
	switch s {
	case Killed:
		return "killed"
	case Survived:
		return "survived"
	default:
		return "equivalent"
	}
}

// Result classifies one mutant. For Killed, Witness is the killing test;
// for Survived, Witness is a counterexample input inside the domain where
// original and mutant disagree. Orig/Mut are the outputs at Witness.
type Result struct {
	Mutant  Mutant
	Status  Status
	Witness map[string]int8
	Orig    int8
	Mut     int8
}

// domain enumerates every input combination: each declared input variable
// ranges over values (in ascending order), later variables vary fastest.
func domain(inputs []string, values []int8) func(func(map[string]int8) bool) {
	return func(yield func(map[string]int8) bool) {
		cur := make(map[string]int8, len(inputs))
		var rec func(i int) bool
		rec = func(i int) bool {
			if i == len(inputs) {
				cp := make(map[string]int8, len(inputs))
				for k, v := range cur {
					cp[k] = v
				}
				return yield(cp)
			}
			for _, v := range values {
				cur[inputs[i]] = v
				if !rec(i + 1) {
					return false
				}
			}
			return true
		}
		rec(0)
	}
}

// analyze classifies every mutant of prog:
//  1. Killed     - some test input makes original and mutant disagree.
//  2. Equivalent - outputs are identical on the whole input domain.
//  3. Survived   - not killed, not equivalent; a domain counterexample
//     (missed by the test suite) is reported.
func analyze(prog []Stmt, tests []map[string]int8, values []int8) ([]Result, error) {
	inputs := inputNames(prog)
	if len(inputs) == 0 {
		return nil, fmt.Errorf("program declares no inputs")
	}
	// Precompute original outputs on the tests.
	origTest := make([]int8, len(tests))
	for i, t := range tests {
		v, err := run(prog, t)
		if err != nil {
			return nil, fmt.Errorf("test %d: %v", i+1, err)
		}
		origTest[i] = v
	}

	muts := genMutants(prog)
	results := make([]Result, 0, len(muts))
	for _, m := range muts {
		mp := applyMutant(prog, m)
		res := Result{Mutant: m, Status: Equivalent}

		// 1. Test suite.
		for i, t := range tests {
			mv, err := run(mp, t)
			if err != nil {
				return nil, fmt.Errorf("mutant %d on test %d: %v", m.ID, i+1, err)
			}
			if mv != origTest[i] {
				res.Status = Killed
				res.Witness = t
				res.Orig, res.Mut = origTest[i], mv
				break
			}
		}

		// 2./3. Exhaustive equivalence check over the finite domain.
		if res.Status != Killed {
			domain(inputs, values)(func(in map[string]int8) bool {
				ov, err := run(prog, in)
				if err != nil {
					return true // cannot happen: domain covers all inputs
				}
				mv, err := run(mp, in)
				if err != nil {
					return true
				}
				if ov != mv {
					res.Status = Survived
					res.Witness = in
					res.Orig, res.Mut = ov, mv
					return false // stop enumerating
				}
				return true
			})
		}
		results = append(results, res)
	}
	return results, nil
}
