package simplex

import (
	"math/big"
	"testing"
)

// ---- test helpers -------------------------------------------------------

func rat(t *testing.T, s string) *big.Rat {
	t.Helper()
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		t.Fatalf("bad rational %q", s)
	}
	return r
}

func rats(t *testing.T, ss ...string) []*big.Rat {
	t.Helper()
	out := make([]*big.Rat, len(ss))
	for i, s := range ss {
		out[i] = rat(t, s)
	}
	return out
}

func mat(t *testing.T, rows ...[]string) [][]*big.Rat {
	t.Helper()
	out := make([][]*big.Rat, len(rows))
	for i, row := range rows {
		out[i] = rats(t, row...)
	}
	return out
}

func requireRat(t *testing.T, got *big.Rat, want string, what string) {
	t.Helper()
	if got.Cmp(rat(t, want)) != 0 {
		t.Fatalf("%s: got %s, want %s", what, got.RatString(), want)
	}
}

func requireRats(t *testing.T, got []*big.Rat, want []string, what string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: got length %d, want %d", what, len(got), len(want))
	}
	for i := range want {
		requireRat(t, got[i], want[i], what)
	}
}

func dot(a, b []*big.Rat) *big.Rat {
	acc := zero()
	for i := range a {
		acc.Add(acc, new(big.Rat).Mul(a[i], b[i]))
	}
	return acc
}

// checkOptimalCertificates verifies primal feasibility, dual feasibility,
// complementary slackness and strong duality for a solved LP.
func checkOptimalCertificates(t *testing.T, c []*big.Rat, A [][]*big.Rat, b []*big.Rat, res *Result) {
	t.Helper()
	m, n := len(b), len(c)

	// Primal feasibility: A x <= b, x >= 0.
	slack := make([]*big.Rat, m)
	for i := 0; i < m; i++ {
		lhs := dot(A[i], res.X)
		if lhs.Cmp(b[i]) > 0 {
			t.Fatalf("primal infeasible: row %d: %s > %s", i, lhs, b[i])
		}
		slack[i] = new(big.Rat).Sub(b[i], lhs)
	}
	for j := 0; j < n; j++ {
		if res.X[j].Sign() < 0 {
			t.Fatalf("primal infeasible: x_%d = %s < 0", j, res.X[j])
		}
	}

	// Dual feasibility: A^T y >= c, y >= 0.
	dualSlack := make([]*big.Rat, n)
	for j := 0; j < n; j++ {
		lhs := zero()
		for i := 0; i < m; i++ {
			lhs.Add(lhs, new(big.Rat).Mul(A[i][j], res.Dual[i]))
		}
		if lhs.Cmp(c[j]) < 0 {
			t.Fatalf("dual infeasible: (A^T y)_%d = %s < c_%d = %s", j, lhs, j, c[j])
		}
		dualSlack[j] = new(big.Rat).Sub(lhs, c[j])
	}
	for i := 0; i < m; i++ {
		if res.Dual[i].Sign() < 0 {
			t.Fatalf("dual infeasible: y_%d = %s < 0", i, res.Dual[i])
		}
	}

	// Complementary slackness.
	for i := 0; i < m; i++ {
		if new(big.Rat).Mul(res.Dual[i], slack[i]).Sign() != 0 {
			t.Fatalf("complementary slackness violated at constraint %d", i)
		}
	}
	for j := 0; j < n; j++ {
		if new(big.Rat).Mul(res.X[j], dualSlack[j]).Sign() != 0 {
			t.Fatalf("complementary slackness violated at variable %d", j)
		}
	}

	// Strong duality: c^T x = b^T y = reported objective.
	primalObj := dot(c, res.X)
	dualObj := dot(b, res.Dual)
	if primalObj.Cmp(res.Objective) != 0 || dualObj.Cmp(res.Objective) != 0 {
		t.Fatalf("strong duality failed: c^Tx=%s, b^Ty=%s, objective=%s",
			primalObj, dualObj, res.Objective)
	}
}

// ---- tests --------------------------------------------------------------

// Acceptance test: a maximization problem with 3 inequality constraints
// whose exact optimum is 17/4.
//
//	max  2 x1 + x2
//	s.t. x1 + x2 <= 3
//	     x1       <= 5/4
//	          x2 <= 2
//	     x1, x2  >= 0
//
// The unique optimum lies at the intersection of the first two constraints:
// x* = (5/4, 7/4), objective 2*(5/4) + 7/4 = 17/4.
func TestOptimalExactRational(t *testing.T) {
	c := rats(t, "2", "1")
	A := mat(t,
		[]string{"1", "1"},
		[]string{"1", "0"},
		[]string{"0", "1"},
	)
	b := rats(t, "3", "5/4", "2")

	res := Solve(c, A, b)

	if res.Status != Optimal {
		t.Fatalf("status = %s, want Optimal", res.Status)
	}
	// Exact rational optimum: 17/4, no floating-point involved.
	requireRat(t, res.Objective, "17/4", "objective")
	requireRats(t, res.X, []string{"5/4", "7/4"}, "primal x")
	// Dual prices: the third constraint is non-binding, so y_3 = 0.
	requireRats(t, res.Dual, []string{"1", "1", "0"}, "dual y")

	// Dual price proof of optimality: y = (1, 1, 0) satisfies
	//   A^T y = (2, 1) = c   (dual feasibility, tight),
	//   b^T y = 3 + 5/4 = 17/4 = c^T x (strong duality),
	// so x* is certified globally optimal.
	checkOptimalCertificates(t, c, A, b, res)

	// Every basis exchange must be an exact rational pivot.
	if res.Iterations == 0 || len(res.Pivots) != res.Iterations {
		t.Fatalf("expected recorded rational pivots, got %d iterations / %d pivots",
			res.Iterations, len(res.Pivots))
	}
	for _, p := range res.Pivots {
		if p.Value == nil || p.Value.Sign() <= 0 {
			t.Fatalf("invalid pivot element %v at iteration %d", p.Value, p.Iteration)
		}
		if p.Enter == p.Leave {
			t.Fatalf("degenerate basis exchange at iteration %d", p.Iteration)
		}
	}
}

// TestStrongDualityFractionalData exercises non-integer rational data.
func TestStrongDualityFractionalData(t *testing.T) {
	c := rats(t, "3", "2")
	A := mat(t,
		[]string{"1", "1"},
		[]string{"2", "1"},
	)
	b := rats(t, "4", "5")

	res := Solve(c, A, b)
	if res.Status != Optimal {
		t.Fatalf("status = %s, want Optimal", res.Status)
	}
	// Optimum at x = (1, 3): objective 3*1 + 2*3 = 9.
	requireRat(t, res.Objective, "9", "objective")
	requireRats(t, res.X, []string{"1", "3"}, "primal x")
	requireRats(t, res.Dual, []string{"1", "1"}, "dual y")
	checkOptimalCertificates(t, c, A, b, res)
}

// TestUnbounded checks the exact rational unboundedness ray.
func TestUnbounded(t *testing.T) {
	c := rats(t, "1", "1")
	A := mat(t,
		[]string{"1", "-1"},
	)
	b := rats(t, "1")

	res := Solve(c, A, b)
	if res.Status != Unbounded {
		t.Fatalf("status = %s, want Unbounded", res.Status)
	}
	// Ray certificate: d >= 0, A d <= 0, c^T d > 0.
	for j, d := range res.Ray {
		if d.Sign() < 0 {
			t.Fatalf("ray has negative component d_%d = %s", j, d)
		}
	}
	for i := range A {
		if v := dot(A[i], res.Ray); v.Sign() > 0 {
			t.Fatalf("ray violates A d <= 0 at row %d: %s", i, v)
		}
	}
	if v := dot(c, res.Ray); v.Sign() <= 0 {
		t.Fatalf("ray must improve objective, got c^T d = %s", v)
	}
}

// TestInfeasible checks the Farkas certificate of infeasibility.
func TestInfeasible(t *testing.T) {
	// x1 <= 1 and x1 >= 2 cannot both hold.
	c := rats(t, "1")
	A := mat(t,
		[]string{"1"},
		[]string{"-1"},
	)
	b := rats(t, "1", "-2")

	res := Solve(c, A, b)
	if res.Status != Infeasible {
		t.Fatalf("status = %s, want Infeasible", res.Status)
	}
	// Farkas certificate: y >= 0, A^T y >= 0, b^T y < 0.
	y := res.Farkas
	for i := range y {
		if y[i].Sign() < 0 {
			t.Fatalf("Farkas y_%d = %s < 0", i, y[i])
		}
	}
	for j := range c {
		lhs := zero()
		for i := range y {
			lhs.Add(lhs, new(big.Rat).Mul(A[i][j], y[i]))
		}
		if lhs.Sign() < 0 {
			t.Fatalf("Farkas (A^T y)_%d = %s < 0", j, lhs)
		}
	}
	if v := dot(b, y); v.Sign() >= 0 {
		t.Fatalf("Farkas certificate needs b^T y < 0, got %s", v)
	}
}

// TestPhase1Feasible uses a constraint with negative RHS, forcing phase 1.
func TestPhase1Feasible(t *testing.T) {
	// max x1 s.t. x1 >= 1 (i.e. -x1 <= -1) and x1 <= 3.
	c := rats(t, "1")
	A := mat(t,
		[]string{"-1"},
		[]string{"1"},
	)
	b := rats(t, "-1", "3")

	res := Solve(c, A, b)
	if res.Status != Optimal {
		t.Fatalf("status = %s, want Optimal", res.Status)
	}
	requireRat(t, res.Objective, "3", "objective")
	requireRats(t, res.X, []string{"3"}, "primal x")
	requireRats(t, res.Dual, []string{"0", "1"}, "dual y")
	checkOptimalCertificates(t, c, A, b, res)
}

// TestCycling: Beale's classic cycling example. With exact rational
// arithmetic and Bland's rule the solver must terminate at the known
// optimum 1/20 instead of cycling forever.
func TestCycling(t *testing.T) {
	c := rats(t, "3/4", "-150", "1/50", "-6")
	A := mat(t,
		[]string{"1/4", "-60", "-1/25", "9"},
		[]string{"1/2", "-90", "-1/50", "3"},
		[]string{"0", "0", "1", "0"},
	)
	b := rats(t, "0", "0", "1")

	res := Solve(c, A, b)
	if res.Status != Optimal {
		t.Fatalf("status = %s, want Optimal", res.Status)
	}
	requireRat(t, res.Objective, "1/20", "objective")
	requireRats(t, res.X, []string{"1/25", "0", "1", "0"}, "primal x")
	checkOptimalCertificates(t, c, A, b, res)
}

// TestDegenerateVertex: several constraints meet at one vertex.
func TestDegenerateVertex(t *testing.T) {
	c := rats(t, "1", "1")
	A := mat(t,
		[]string{"1", "0"},
		[]string{"0", "1"},
		[]string{"1", "1"},
	)
	b := rats(t, "2", "2", "2")

	res := Solve(c, A, b)
	if res.Status != Optimal {
		t.Fatalf("status = %s, want Optimal", res.Status)
	}
	requireRat(t, res.Objective, "2", "objective")
	checkOptimalCertificates(t, c, A, b, res)
}
