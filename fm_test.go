package main

import (
	"fmt"
	"math/big"
	"testing"
)

func mkSys(numVars int, spec ...interface{}) []*Constraint {
	// spec entries: coeffs []string, const string, kind
	sys := []*Constraint{}
	for i := 0; i+2 < len(spec); i += 3 {
		coeffs := spec[i].([]string)
		konst := spec[i+1].(string)
		kind := spec[i+2].(Kind)
		c := newConstraint(numVars, kind, []int{i / 3})
		for j, s := range coeffs {
			c.Coeff[j] = rat(s)
		}
		c.Const = rat(konst)
		sys = append(sys, c)
	}
	return sys
}

func bigBudget() Budget { return Budget{MaxConstraints: 1 << 20, MaxOps: 1 << 30} }

func findConstraint(sys []*Constraint, want string) *Constraint {
	for _, c := range sys {
		if c.String() == want || keyOf(c) == want {
			return c
		}
	}
	return nil
}

func keyOf(c *Constraint) string {
	s := ""
	for _, r := range c.Coeff {
		s += r.RatString() + ","
	}
	s += c.Const.RatString() + c.Kind.String()
	return s
}

func TestFMBasicProjection(t *testing.T) {
	// x+y<=2, x-y<=0, -y<=0; eliminate y (index 1)
	// expect projection x<=1 (source {0,1}); x<=2 (source {0,2}) redundant.
	sys := mkSys(2,
		[]string{"1", "1"}, "-2", Le,
		[]string{"1", "-1"}, "0", Le,
		[]string{"0", "-1"}, "0", Le,
	)
	eng := &Engine{NumVars: 2, Budget: bigBudget(), Prune: true}
	res := eng.Eliminate(sys, []int{1})
	if res.Status != StatusOK || !res.Feasible {
		t.Fatalf("status=%s feasible=%v", res.Status, res.Feasible)
	}
	if len(res.Constraints) != 1 {
		t.Fatalf("got %d constraints: %v", len(res.Constraints), res.Constraints)
	}
	c := res.Constraints[0]
	// x - 1 <= 0
	if c.Coeff[0].Cmp(big.NewRat(1, 1)) != 0 || c.Coeff[1].Sign() != 0 ||
		c.Const.Cmp(big.NewRat(-1, 1)) != 0 || c.Kind != Le {
		t.Fatalf("unexpected constraint %s", c)
	}
	if len(c.Source) != 2 || c.Source[0] != 0 || c.Source[1] != 1 {
		t.Fatalf("source=%v want [0 1]", c.Source)
	}
	// redundant x<=2 must be retained as evidence
	found := false
	for _, rm := range res.Removed {
		if rm.Reason == "implied_by_others" && rm.C != nil &&
			rm.C.Const.Cmp(big.NewRat(-2, 1)) == 0 {
			if len(rm.C.Source) != 2 || rm.C.Source[0] != 0 || rm.C.Source[1] != 2 {
				t.Fatalf("removed source=%v want [0 2]", rm.C.Source)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("redundant x<=2 not retained in removed evidence")
	}
}

func TestFMStrictnessPropagation(t *testing.T) {
	// y < x  (y - x < 0), y > 0 (-y < 0); eliminate y => -x < 0 (strict)
	sys := mkSys(2,
		[]string{"-1", "1"}, "0", Lt,
		[]string{"0", "-1"}, "0", Lt,
	)
	eng := &Engine{NumVars: 2, Budget: bigBudget()}
	res := eng.Eliminate(sys, []int{1})
	if len(res.Constraints) != 1 || res.Constraints[0].Kind != Lt {
		t.Fatalf("want single strict constraint, got %v", res.Constraints)
	}
	if res.Constraints[0].Coeff[0].Cmp(big.NewRat(-1, 1)) != 0 {
		t.Fatalf("got %s", res.Constraints[0])
	}

	// mixed: one strict parent suffices
	sys = mkSys(2,
		[]string{"-1", "1"}, "0", Le,
		[]string{"0", "-1"}, "0", Lt,
	)
	res = (&Engine{NumVars: 2, Budget: bigBudget()}).Eliminate(sys, []int{1})
	if len(res.Constraints) != 1 || res.Constraints[0].Kind != Lt {
		t.Fatalf("mixed parents should give strict result, got %v", res.Constraints)
	}

	// both non-strict => non-strict
	sys = mkSys(2,
		[]string{"-1", "1"}, "0", Le,
		[]string{"0", "-1"}, "0", Le,
	)
	res = (&Engine{NumVars: 2, Budget: bigBudget()}).Eliminate(sys, []int{1})
	if len(res.Constraints) != 1 || res.Constraints[0].Kind != Le {
		t.Fatalf("non-strict parents should give non-strict result, got %v", res.Constraints)
	}
}

func TestFMEqualitySubstitution(t *testing.T) {
	// x + y = 3, x - y <= 1; eliminate y => x <= 2
	sys := mkSys(2,
		[]string{"1", "1"}, "-3", Eq,
		[]string{"1", "-1"}, "-1", Le,
	)
	eng := &Engine{NumVars: 2, Budget: bigBudget()}
	res := eng.Eliminate(sys, []int{1})
	if len(res.Constraints) != 1 {
		t.Fatalf("got %v", res.Constraints)
	}
	c := res.Constraints[0]
	if c.Coeff[0].Cmp(big.NewRat(1, 1)) != 0 || c.Const.Cmp(big.NewRat(-2, 1)) != 0 || c.Kind != Le {
		t.Fatalf("got %s", c)
	}
	// equality among kept vars stays in the output
	sys = mkSys(2,
		[]string{"1", "1"}, "-3", Eq,
		[]string{"1", "-1"}, "-1", Le,
	)
	res = eng.Eliminate(sys, []int{})
	hasEq := false
	for _, c := range res.Constraints {
		if c.Kind == Eq {
			hasEq = true
		}
	}
	if !hasEq {
		t.Fatal("kept-variable equality must remain in output")
	}
}

func TestFMInfeasible(t *testing.T) {
	// x <= 0 and x >= 1; eliminate x => contradiction
	sys := mkSys(1,
		[]string{"1"}, "0", Le,
		[]string{"-1"}, "1", Le,
	)
	eng := &Engine{NumVars: 1, Budget: bigBudget()}
	res := eng.Eliminate(sys, []int{0})
	if res.Feasible {
		t.Fatal("expected infeasible")
	}
	if len(res.Constraints) != 1 || !res.Constraints[0].isZeroRow() {
		t.Fatalf("expected contradiction witness, got %v", res.Constraints)
	}
	rep := Verify(sys, 1, nil, res.Constraints, res.Feasible)
	if !rep.OK {
		t.Fatalf("verification of empty projection failed: %+v", rep)
	}
}

func TestFMUnbounded(t *testing.T) {
	// x - y <= 0; eliminate y => no constraints (whole line), unbounded
	sys := mkSys(2,
		[]string{"1", "-1"}, "0", Le,
	)
	eng := &Engine{NumVars: 2, Budget: bigBudget()}
	res := eng.Eliminate(sys, []int{1})
	if !res.Feasible || len(res.Constraints) != 0 {
		t.Fatalf("feasible=%v constraints=%v", res.Feasible, res.Constraints)
	}
	rep := Verify(sys, 2, []int{0}, res.Constraints, res.Feasible)
	if !rep.OK {
		t.Fatalf("verification failed: %+v", rep)
	}
}

func TestFMBudgetExhaustion(t *testing.T) {
	// 4 lower + 4 upper bounds on y => 16 combinations; budget 10 must trip.
	spec := []interface{}{}
	for i := 1; i <= 4; i++ {
		spec = append(spec, []string{"0", "-1"}, fmt.Sprintf("%d", i), Le) // y >= i
	}
	for i := 1; i <= 4; i++ {
		spec = append(spec, []string{"0", "1"}, fmt.Sprintf("%d", i), Le) // y <= -i
	}
	sys := mkSys(2, spec...)
	eng := &Engine{NumVars: 2, Budget: Budget{MaxConstraints: 10, MaxOps: 1 << 30}}
	res := eng.Eliminate(sys, []int{1})
	if res.Status != StatusBudgetExceeded {
		t.Fatalf("status=%s want budget_exceeded", res.Status)
	}
	// must NOT claim a completed projection; y must still be present
	if len(res.Uneliminated) == 0 {
		t.Fatal("budget-exhausted result must list uneliminated variables")
	}
	stillThere := false
	for _, c := range res.Constraints {
		if c.Coeff[1].Sign() != 0 {
			stillThere = true
		}
	}
	if !stillThere {
		t.Fatal("budget-exhausted result dropped the eliminated variable: fake projection")
	}
}

func TestFMOppositeOpsAndRationalCoeffs(t *testing.T) {
	// (2/3)x + y >= 1/2  =>  -(2/3)x - y + 1/2 <= 0; eliminate y with
	// y <= 2 - x  =>  -(2/3)x + 1/2 <= 2 - x  =>  (1/3)x <= 3/2  =>  x <= 9/2
	sys := mkSys(2,
		[]string{"-2/3", "-1"}, "1/2", Le,
		[]string{"1", "1"}, "-2", Le,
	)
	eng := &Engine{NumVars: 2, Budget: bigBudget()}
	res := eng.Eliminate(sys, []int{1})
	if len(res.Constraints) != 1 {
		t.Fatalf("got %v", res.Constraints)
	}
	c := res.Constraints[0]
	// normalized primitive form: 2x - 9 <= 0
	if c.Coeff[0].Cmp(rat("2")) != 0 || c.Const.Cmp(rat("-9")) != 0 {
		t.Fatalf("got %s", c)
	}
	rep := Verify(sys, 2, []int{0}, res.Constraints, res.Feasible)
	if !rep.OK {
		t.Fatalf("verification failed: %+v", rep)
	}
}

func TestFMRandomSystemsVerify(t *testing.T) {
	rng := newRand(7)
	for trial := 0; trial < 25; trial++ {
		n := 2 + rng.intn(3) // 2..4 vars
		m := 3 + rng.intn(4) // 3..6 constraints
		sys := randomSystem(rng, n, m)
		// eliminate about half the variables
		var elim, kept []int
		for v := 0; v < n; v++ {
			if v > 0 && rng.intn(2) == 0 {
				elim = append(elim, v)
			} else {
				kept = append(kept, v)
			}
		}
		eng := &Engine{NumVars: n, Budget: bigBudget(), Prune: true}
		res := eng.Eliminate(sys, elim)
		if res.Status != StatusOK {
			t.Fatalf("trial %d: status=%s", trial, res.Status)
		}
		rep := Verify(sys, n, kept, res.Constraints, res.Feasible)
		if !rep.OK {
			for _, c := range sys {
				t.Logf("  sys: %s", c)
			}
			t.Fatalf("trial %d: verification failed: %+v", trial, rep)
		}
	}
}
