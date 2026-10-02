package simplex

import (
	"math/big"
	"testing"
)

func mustSolve(t *testing.T, lp *LP) *Result {
	t.Helper()
	res, err := Solve(lp)
	if err != nil {
		t.Fatalf("Solve returned error: %v", err)
	}
	return res
}

func ratEq(t *testing.T, got *big.Rat, want string) {
	t.Helper()
	if got.Cmp(Rat(want)) != 0 {
		t.Fatalf("got %s, want %s", got.RatString(), want)
	}
}

func dot(a, b []*big.Rat) *big.Rat {
	sum := new(big.Rat)
	for i := range a {
		sum.Add(sum, new(big.Rat).Mul(a[i], b[i]))
	}
	return sum
}

// checkPrimalFeasible verifies x >= 0 and A x <= B exactly.
func checkPrimalFeasible(t *testing.T, lp *LP, x []*big.Rat) {
	t.Helper()
	for j, xj := range x {
		if xj.Sign() < 0 {
			t.Fatalf("primal infeasible: x[%d] = %s < 0", j, xj)
		}
	}
	for i := range lp.A {
		lhs := dot(lp.A[i], x)
		if lhs.Cmp(lp.B[i]) > 0 {
			t.Fatalf("constraint %d violated: %s > %s", i, lhs, lp.B[i])
		}
	}
}

// checkDualProof verifies the dual-price optimality certificate:
// y >= 0, A^T y >= C (dual feasibility) and B^T y == C^T x (strong duality).
func checkDualProof(t *testing.T, lp *LP, res *Result) {
	t.Helper()
	for i, yi := range res.Dual {
		if yi.Sign() < 0 {
			t.Fatalf("dual infeasible: y[%d] = %s < 0", i, yi)
		}
	}
	n := len(lp.C)
	for j := 0; j < n; j++ {
		lhs := new(big.Rat)
		for i := range lp.A {
			lhs.Add(lhs, new(big.Rat).Mul(lp.A[i][j], res.Dual[i]))
		}
		if lhs.Cmp(lp.C[j]) < 0 {
			t.Fatalf("dual constraint %d violated: %s < %s", j, lhs, lp.C[j])
		}
	}
	primalObj := dot(lp.C, res.Primal)
	dualObj := dot(lp.B, res.Dual)
	if primalObj.Cmp(dualObj) != 0 {
		t.Fatalf("strong duality failed: C^T x = %s != B^T y = %s", primalObj, dualObj)
	}
	if primalObj.Cmp(res.Objective) != 0 {
		t.Fatalf("reported objective %s != C^T x = %s", res.Objective, primalObj)
	}
	// Complementary slackness: y_i (A_i x - b_i) = 0 for every constraint.
	for i := range lp.A {
		slack := new(big.Rat).Sub(dot(lp.A[i], res.Primal), lp.B[i])
		prod := new(big.Rat).Mul(res.Dual[i], slack)
		if prod.Sign() != 0 {
			t.Fatalf("complementary slackness violated at constraint %d", i)
		}
	}
}

// Acceptance test: a maximization problem with 3 inequality constraints.
//
//	max 2x + 3y
//	s.t. x +  y <= 3/2
//	     x + 2y <= 11/4
//	     3x +  y <= 3
//	     x, y >= 0
//
// The exact optimum is 17/4 at (x, y) = (1/4, 5/4) with dual prices
// (1, 1, 0).
func TestMaximizeThreeConstraints(t *testing.T) {
	lp := &LP{
		A: [][]*big.Rat{
			{Rat("1"), Rat("1")},
			{Rat("1"), Rat("2")},
			{Rat("3"), Rat("1")},
		},
		B: []*big.Rat{Rat("3/2"), Rat("11/4"), Rat("3")},
		C: []*big.Rat{Rat("2"), Rat("3")},
	}
	res := mustSolve(t, lp)
	if res.Status != Optimal {
		t.Fatalf("status = %s, want Optimal", res.Status)
	}
	ratEq(t, res.Objective, "17/4")
	ratEq(t, res.Primal[0], "1/4")
	ratEq(t, res.Primal[1], "5/4")
	ratEq(t, res.Dual[0], "1")
	ratEq(t, res.Dual[1], "1")
	ratEq(t, res.Dual[2], "0")
	checkPrimalFeasible(t, lp, res.Primal)
	checkDualProof(t, lp, res)
}

// TestExactThirds uses answers like 1/3 that binary floating point cannot
// represent, demonstrating lossless rational arithmetic.
func TestExactThirds(t *testing.T) {
	lp := &LP{
		A: [][]*big.Rat{
			{Rat("1"), Rat("2")},
			{Rat("2"), Rat("1")},
		},
		B: []*big.Rat{Rat("1"), Rat("1")},
		C: []*big.Rat{Rat("1"), Rat("1")},
	}
	res := mustSolve(t, lp)
	if res.Status != Optimal {
		t.Fatalf("status = %s, want Optimal", res.Status)
	}
	ratEq(t, res.Objective, "2/3")
	ratEq(t, res.Primal[0], "1/3")
	ratEq(t, res.Primal[1], "1/3")
	checkPrimalFeasible(t, lp, res.Primal)
	checkDualProof(t, lp, res)
}

// TestUnbounded checks the unboundedness certificate: a feasible point plus
// a ray d >= 0 with A d <= 0 and C^T d > 0.
func TestUnbounded(t *testing.T) {
	lp := &LP{
		A: [][]*big.Rat{{Rat("1"), Rat("-1")}},
		B: []*big.Rat{Rat("1")},
		C: []*big.Rat{Rat("1"), Rat("1")},
	}
	res := mustSolve(t, lp)
	if res.Status != Unbounded {
		t.Fatalf("status = %s, want Unbounded", res.Status)
	}
	checkPrimalFeasible(t, lp, res.Primal)
	for j, dj := range res.Ray {
		if dj.Sign() < 0 {
			t.Fatalf("ray has negative component d[%d] = %s", j, dj)
		}
	}
	for i := range lp.A {
		if v := dot(lp.A[i], res.Ray); v.Sign() > 0 {
			t.Fatalf("ray violates A d <= 0 at row %d: %s", i, v)
		}
	}
	if v := dot(lp.C, res.Ray); v.Sign() <= 0 {
		t.Fatalf("ray must improve objective, got C^T d = %s", v)
	}
}

// TestInfeasible checks the Farkas certificate y >= 0, y^T A >= 0, y^T B < 0.
func TestInfeasible(t *testing.T) {
	// x + y <= 1 and x + y >= 2 cannot hold simultaneously.
	lp := &LP{
		A: [][]*big.Rat{
			{Rat("1"), Rat("1")},
			{Rat("-1"), Rat("-1")},
		},
		B: []*big.Rat{Rat("1"), Rat("-2")},
		C: []*big.Rat{Rat("1"), Rat("1")},
	}
	res := mustSolve(t, lp)
	if res.Status != Infeasible {
		t.Fatalf("status = %s, want Infeasible", res.Status)
	}
	y := res.Farkas
	for i, yi := range y {
		if yi.Sign() < 0 {
			t.Fatalf("Farkas y[%d] = %s < 0", i, yi)
		}
	}
	n := len(lp.C)
	for j := 0; j < n; j++ {
		lhs := new(big.Rat)
		for i := range lp.A {
			lhs.Add(lhs, new(big.Rat).Mul(y[i], lp.A[i][j]))
		}
		if lhs.Sign() < 0 {
			t.Fatalf("Farkas y^T A has negative component %d: %s", j, lhs)
		}
	}
	if v := dot(y, lp.B); v.Sign() >= 0 {
		t.Fatalf("Farkas y^T B must be negative, got %s", v)
	}
}

// TestPhaseOneFeasible exercises phase 1 (negative right-hand side) on a
// feasible problem: max x s.t. x + y <= 4 and x + y >= 1.
func TestPhaseOneFeasible(t *testing.T) {
	lp := &LP{
		A: [][]*big.Rat{
			{Rat("1"), Rat("1")},
			{Rat("-1"), Rat("-1")},
		},
		B: []*big.Rat{Rat("4"), Rat("-1")},
		C: []*big.Rat{Rat("1"), Rat("1")},
	}
	res := mustSolve(t, lp)
	if res.Status != Optimal {
		t.Fatalf("status = %s, want Optimal", res.Status)
	}
	ratEq(t, res.Objective, "4")
	checkPrimalFeasible(t, lp, res.Primal)
	checkDualProof(t, lp, res)
}

// TestBealeCycling is Beale's classic degenerate example on which the
// naive largest-coefficient rule cycles forever. Bland's rule terminates
// with the exact optimum 1/20.
func TestBealeCycling(t *testing.T) {
	lp := &LP{
		A: [][]*big.Rat{
			{Rat("1/4"), Rat("-60"), Rat("-1/25"), Rat("9")},
			{Rat("1/2"), Rat("-90"), Rat("-1/50"), Rat("3")},
			{Rat("0"), Rat("0"), Rat("1"), Rat("0")},
		},
		B: []*big.Rat{Rat("0"), Rat("0"), Rat("1")},
		C: []*big.Rat{Rat("3/4"), Rat("-150"), Rat("1/50"), Rat("-6")},
	}
	res := mustSolve(t, lp)
	if res.Status != Optimal {
		t.Fatalf("status = %s, want Optimal", res.Status)
	}
	ratEq(t, res.Objective, "1/20")
	checkPrimalFeasible(t, lp, res.Primal)
	checkDualProof(t, lp, res)
}

// TestDegenerateRedundant covers a redundant constraint row, which phase 1
// must drop cleanly.
func TestDegenerateRedundant(t *testing.T) {
	// Constraint 2 is constraint 1 scaled by 2; both have negative RHS
	// after rewriting x + y >= 1, forcing phase 1 with a redundant row.
	// Optimum: y = 3, x = 0, objective 6.
	lp := &LP{
		A: [][]*big.Rat{
			{Rat("-1"), Rat("-1")},
			{Rat("-2"), Rat("-2")},
			{Rat("1"), Rat("1")},
		},
		B: []*big.Rat{Rat("-1"), Rat("-2"), Rat("3")},
		C: []*big.Rat{Rat("1"), Rat("2")},
	}
	res := mustSolve(t, lp)
	if res.Status != Optimal {
		t.Fatalf("status = %s, want Optimal", res.Status)
	}
	ratEq(t, res.Objective, "6")
	checkPrimalFeasible(t, lp, res.Primal)
	checkDualProof(t, lp, res)
}
