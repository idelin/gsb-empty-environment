package netsec

import (
	"math/rand"
	"testing"
)

func TestSolveCNFBasic(t *testing.T) {
	cases := []struct {
		name string
		f    CNF
		want bool
	}{
		{"empty formula", CNF{}, true},
		{"empty clause", CNF{{}}, false},
		{"unit", CNF{{Lit(1)}}, true},
		{"contradictory units", CNF{{Lit(1)}, {-Lit(1)}}, false},
		{"two options", CNF{{Lit(1), Lit(2)}, {-Lit(1)}, {-Lit(2)}}, false},
		{"choose branch", CNF{{Lit(1), Lit(2)}, {-Lit(1), Lit(2)}, {Lit(1), -Lit(2)}}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, model := solveCNF(tc.f, nil)
			if got != tc.want {
				t.Fatalf("solveCNF = %v, want %v", got, tc.want)
			}
			if got && !(&Witness{Assignment: model}).Satisfies(tc.f) {
				t.Fatalf("returned model does not satisfy formula: %v", model)
			}
		})
	}
}

func TestSolveCNFAssumptions(t *testing.T) {
	f := CNF{{Lit(1), Lit(2)}, {-Lit(1), -Lit(2)}}

	if sat, _ := solveCNF(f, []Literal{Lit(1), Lit(2)}); sat {
		t.Fatal("conflicting assumptions reported satisfiable")
	}
	sat, model := solveCNF(f, []Literal{Lit(1)})
	if !sat {
		t.Fatal("feasible assumption reported unsatisfiable")
	}
	if !model[1] || model[2] {
		t.Fatalf("assumption not honored by model: %v", model)
	}
}

// TestSolverDifferential runs the two deliberately independent SAT engines
// against hundreds of shared random instances, including assumption sets.
func TestSolverDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(20261118))

	for seed := 0; seed < 400; seed++ {
		nVars := 1 + rng.Intn(8)
		f := randomCNF(rng, nVars, 2+rng.Intn(12), 1+rng.Intn(3))

		var assumptions []Literal
		if rng.Intn(2) == 0 {
			for v := 1; v <= nVars; v++ {
				if rng.Intn(2) == 0 {
					l := Literal(v)
					if rng.Intn(2) == 0 {
						l = -l
					}
					assumptions = append(assumptions, l)
				}
			}
		}

		gotA, modelA := solveCNF(f, assumptions)
		gotB, _ := indSolve(f, assumptions)
		if gotA != gotB {
			t.Fatalf("seed %d: solver disagreement (%v vs %v) on %v assumptions %v",
				seed, gotA, gotB, f, assumptions)
		}
		if gotA && !(&Witness{Assignment: modelA}).Satisfies(f) {
			t.Fatalf("seed %d: construction model violates formula", seed)
		}
		if gotA {
			for _, l := range assumptions {
				if modelA[l.Var()] != l.IsPositive() {
					t.Fatalf("seed %d: model violates assumption %v", seed, l)
				}
			}
		}
	}
}

// randomCNF builds a random CNF with nVars variables, nClauses clauses and
// clause widths up to width.
func randomCNF(rng *rand.Rand, nVars, nClauses, width int) CNF {
	f := make(CNF, 0, nClauses)
	for i := 0; i < nClauses; i++ {
		w := 1 + rng.Intn(width)
		c := make(Clause, 0, w)
		for j := 0; j < w; j++ {
			v := 1 + rng.Intn(nVars)
			l := Literal(v)
			if rng.Intn(2) == 0 {
				l = -l
			}
			c = append(c, l)
		}
		f = append(f, c)
	}
	return f
}
