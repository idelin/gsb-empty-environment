package main

import (
	"math/big"
	"testing"
)

func rat(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("bad rat " + s)
	}
	return r
}

func rats(ss ...string) []*big.Rat {
	out := make([]*big.Rat, len(ss))
	for i, s := range ss {
		out[i] = rat(s)
	}
	return out
}

func TestSimplexKnownOptimum(t *testing.T) {
	// max x+y s.t. x+2y<=4, 4x+2y<=12, optimum 10/3 at (8/3,2/3)
	A := [][]*big.Rat{rats("1", "2"), rats("4", "2")}
	b := rats("4", "12")
	c := rats("1", "1")
	res := SolveLP(A, b, c)
	if res.Status != LPOptimal {
		t.Fatalf("status=%s", res.Status)
	}
	if res.Value.Cmp(rat("10/3")) != 0 {
		t.Fatalf("value=%s want 10/3", res.Value)
	}
	if res.Point[0].Cmp(rat("8/3")) != 0 || res.Point[1].Cmp(rat("2/3")) != 0 {
		t.Fatalf("point=(%s,%s)", res.Point[0], res.Point[1])
	}
}

func TestSimplexFreeVariable(t *testing.T) {
	// max x s.t. x <= 5/2, x free
	res := SolveLP([][]*big.Rat{rats("1")}, rats("5/2"), rats("1"))
	if res.Status != LPOptimal || res.Value.Cmp(rat("5/2")) != 0 {
		t.Fatalf("got %s %s", res.Status, res.Value)
	}
	// min x s.t. x >= -3 (free var, negative optimum)
	res = SolveLP([][]*big.Rat{rats("-1")}, rats("3"), rats("-1"))
	if res.Status != LPOptimal || res.Value.Cmp(rat("3")) != 0 {
		t.Fatalf("got %s %s", res.Status, res.Value)
	}
	if res.Point[0].Cmp(rat("-3")) != 0 {
		t.Fatalf("point=%s want -3", res.Point[0])
	}
}

func TestSimplexUnbounded(t *testing.T) {
	res := SolveLP([][]*big.Rat{rats("1", "-1")}, rats("0"), rats("1", "0"))
	if res.Status != LPUnbounded {
		t.Fatalf("status=%s want unbounded", res.Status)
	}
}

func TestSimplexInfeasible(t *testing.T) {
	res := SolveLP([][]*big.Rat{rats("1"), rats("-1")}, rats("1", "-2"), rats("1"))
	if res.Status != LPInfeasible {
		t.Fatalf("status=%s want infeasible", res.Status)
	}
}

func TestFeasibleMixedStrict(t *testing.T) {
	// x < 0 and x > -1 (i.e. -x - 1 < 0): feasible strictly
	mk := func(coeff, konst string, k Kind) *Constraint {
		c := newConstraint(1, k, nil)
		c.Coeff[0] = rat(coeff)
		c.Const = rat(konst)
		return c
	}
	if !FeasibleMixed([]*Constraint{mk("1", "0", Lt), mk("-1", "-1", Lt)}, 1) {
		t.Fatal("x<0, x>-1 should be feasible")
	}
	// x < 0 and x >= 0: infeasible
	if FeasibleMixed([]*Constraint{mk("1", "0", Lt), mk("-1", "0", Le)}, 1) {
		t.Fatal("x<0, x>=0 should be infeasible")
	}
	// x <= 0 and x >= 0: feasible (x=0)
	if !FeasibleMixed([]*Constraint{mk("1", "0", Le), mk("-1", "0", Le)}, 1) {
		t.Fatal("x<=0, x>=0 should be feasible")
	}
	// x < 0 and x < -1 and x > -1/2: infeasible
	if FeasibleMixed([]*Constraint{mk("1", "0", Lt), mk("1", "1", Lt), mk("-1", "-1/2", Lt)}, 1) {
		t.Fatal("contradictory strict bounds should be infeasible")
	}
}

// fmFeasible decides feasibility by eliminating every variable (independent
// of the simplex method); used to cross-check the LP solver.
func fmFeasible(sys []*Constraint, numVars int) bool {
	eng := &Engine{NumVars: numVars, Budget: Budget{MaxConstraints: 1 << 20, MaxOps: 1 << 30}}
	elim := make([]int, numVars)
	for i := range elim {
		elim[i] = i
	}
	return eng.Eliminate(sys, elim).Feasible
}

func TestSimplexVsFMRandomFeasibility(t *testing.T) {
	rng := newRand(42)
	for trial := 0; trial < 150; trial++ {
		n := 1 + rng.intn(3)
		m := 1 + rng.intn(5)
		sys := randomSystem(rng, n, m)
		gotLP := FeasibleMixed(sys, n)
		gotFM := fmFeasible(sys, n)
		if gotLP != gotFM {
			t.Fatalf("trial %d: LP=%v FM=%v\nsystem:", trial, gotLP, gotFM)
		}
	}
}
