package rootiso

import (
	"math/big"
	"testing"
)

// --- helpers ---------------------------------------------------------------

func ints(c ...int64) []*big.Int {
	out := make([]*big.Int, len(c))
	for i, v := range c {
		out[i] = big.NewInt(v)
	}
	return out
}

func rat(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("bad rational: " + s)
	}
	return r
}

// lin returns q*x - p, whose root is p/q.
func lin(p, q int64) []*big.Int { return ints(-p, q) }

func mulPoly(a, b []*big.Int) []*big.Int {
	out := make([]*big.Int, len(a)+len(b)-1)
	for i := range out {
		out[i] = new(big.Int)
	}
	for i, ai := range a {
		for j, bj := range b {
			out[i+j].Add(out[i+j], new(big.Int).Mul(ai, bj))
		}
	}
	return out
}

func product(factors ...[]*big.Int) []*big.Int {
	acc := ints(1)
	for _, f := range factors {
		acc = mulPoly(acc, f)
	}
	return acc
}

// checkDisjoint verifies the emitted open intervals are pairwise disjoint
// and that no reported point root lies inside any isolating interval.
func checkDisjoint(t *testing.T, res *Result) {
	t.Helper()
	for i := 1; i < len(res.Intervals); i++ {
		prev, cur := res.Intervals[i-1], res.Intervals[i]
		if prev.Hi.Cmp(cur.Lo) > 0 {
			t.Fatalf("intervals overlap: (%s,%s) vs (%s,%s)",
				prev.Lo, prev.Hi, cur.Lo, cur.Hi)
		}
	}
	for _, p := range res.Points {
		for _, iv := range res.Intervals {
			if iv.Lo.Cmp(p) < 0 && p.Cmp(iv.Hi) < 0 {
				t.Fatalf("point root %s lies inside interval (%s,%s)", p, iv.Lo, iv.Hi)
			}
		}
	}
}

// checkEvidence verifies every interval carries a genuine sign change.
func checkEvidence(t *testing.T, res *Result) {
	t.Helper()
	for _, iv := range res.Intervals {
		if iv.SignLo == 0 || iv.SignHi == 0 || iv.SignLo == iv.SignHi {
			t.Fatalf("interval (%s,%s) lacks sign-change evidence: %d,%d",
				iv.Lo, iv.Hi, iv.SignLo, iv.SignHi)
		}
	}
}

func intervalContains(res *Result, r *big.Rat) bool {
	for _, iv := range res.Intervals {
		if iv.Lo.Cmp(r) < 0 && r.Cmp(iv.Hi) < 0 {
			return true
		}
	}
	return false
}

func hasPoint(res *Result, r *big.Rat) bool {
	for _, p := range res.Points {
		if p.Cmp(r) == 0 {
			return true
		}
	}
	return false
}

// covered: root r is either an exactly reported point or strictly inside an
// isolating interval.
func covered(res *Result, r *big.Rat) bool {
	return hasPoint(res, r) || intervalContains(res, r)
}

// --- tests -----------------------------------------------------------------

// Product of linear factors with known rational roots.
func TestRationalRootProduct(t *testing.T) {
	// roots: -2, 1/2, 3/4, 5
	p := product(lin(-2, 1), lin(1, 2), lin(3, 4), lin(5, 1))
	res, err := Isolate(p, rat("-3"), rat("6"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Count() != 4 {
		t.Fatalf("want 4 distinct roots, got %d", res.Count())
	}
	if len(res.Points) != 0 {
		t.Fatalf("no endpoint roots expected, got %v", res.Points)
	}
	for _, rs := range []string{"-2", "1/2", "3/4", "5"} {
		if !intervalContains(res, rat(rs)) {
			t.Fatalf("root %s not isolated", rs)
		}
	}
	checkDisjoint(t, res)
	checkEvidence(t, res)
}

// Roots sitting exactly on the interval endpoints must be reported
// separately as exact points, not as intervals.
func TestEndpointRootsReportedSeparately(t *testing.T) {
	p := product(lin(-2, 1), lin(1, 2), lin(3, 4), lin(5, 1))
	res, err := Isolate(p, rat("-2"), rat("5"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Count() != 4 {
		t.Fatalf("want 4 distinct roots, got %d", res.Count())
	}
	if !hasPoint(res, rat("-2")) || !hasPoint(res, rat("5")) {
		t.Fatalf("endpoint roots -2 and 5 must be reported as points, got %v", res.Points)
	}
	if !intervalContains(res, rat("1/2")) || !intervalContains(res, rat("3/4")) {
		t.Fatal("interior roots 1/2 and 3/4 must be isolated in intervals")
	}
	checkDisjoint(t, res)
	checkEvidence(t, res)
}

// Both endpoints are roots of x^2-1; the open interval is then root-free.
func TestBothEndpointsAreRoots(t *testing.T) {
	res, err := Isolate(ints(-1, 0, 1), rat("-1"), rat("1"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Count() != 2 || len(res.Points) != 2 || len(res.Intervals) != 0 {
		t.Fatalf("want exactly the two endpoint roots, got %+v", res)
	}
}

// Multiple roots must be deduplicated: (x-1)^2 (x-2)^3 (x+1) has exactly
// three distinct real roots.
func TestMultipleRootsDeduplicated(t *testing.T) {
	p := product(lin(1, 1), lin(1, 1), lin(2, 1), lin(2, 1), lin(2, 1), lin(-1, 1))
	res, err := Isolate(p, rat("-3"), rat("3"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Count() != 3 {
		t.Fatalf("want 3 distinct roots, got %d (%+v)", res.Count(), res)
	}
	for _, rs := range []string{"-1", "1", "2"} {
		if !covered(res, rat(rs)) {
			t.Fatalf("root %s missing", rs)
		}
	}
	checkDisjoint(t, res)
	checkEvidence(t, res)
}

// An even-multiplicity root gives no sign change in the original polynomial,
// so the evidence must come from the square-free part.
func TestEvenMultiplicitySignEvidence(t *testing.T) {
	p := product(lin(0, 1), lin(0, 1), lin(2, 1)) // x^2 (x-2)
	res, err := Isolate(p, rat("-1"), rat("3"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Count() != 2 {
		t.Fatalf("want 2 distinct roots, got %d", res.Count())
	}
	checkEvidence(t, res) // would fail if signs were taken from p itself
}

// Low-degree polynomials with irrational roots, verified by exact rational
// comparisons (no floating point involved in the verdict).
func TestIrrationalRoots(t *testing.T) {
	t.Run("x^2-2", func(t *testing.T) {
		res, err := Isolate(ints(-2, 0, 1), rat("0"), rat("2"))
		if err != nil {
			t.Fatal(err)
		}
		if res.Count() != 1 || len(res.Intervals) != 1 {
			t.Fatalf("want exactly one root (sqrt 2), got %+v", res)
		}
		iv := res.Intervals[0]
		two := big.NewRat(2, 1)
		lo2 := new(big.Rat).Mul(iv.Lo, iv.Lo)
		hi2 := new(big.Rat).Mul(iv.Hi, iv.Hi)
		if lo2.Cmp(two) >= 0 || hi2.Cmp(two) <= 0 {
			t.Fatalf("interval (%s,%s) does not provably contain sqrt(2)", iv.Lo, iv.Hi)
		}
		if iv.SignLo != -1 || iv.SignHi != 1 {
			t.Fatalf("bad evidence signs %d,%d", iv.SignLo, iv.SignHi)
		}
	})

	t.Run("x^3-2", func(t *testing.T) {
		res, err := Isolate(ints(-2, 0, 0, 1), rat("1"), rat("2"))
		if err != nil {
			t.Fatal(err)
		}
		if res.Count() != 1 {
			t.Fatalf("want exactly one root (cbrt 2), got %+v", res)
		}
		iv := res.Intervals[0]
		two := big.NewRat(2, 1)
		lo3 := new(big.Rat).Mul(new(big.Rat).Mul(iv.Lo, iv.Lo), iv.Lo)
		hi3 := new(big.Rat).Mul(new(big.Rat).Mul(iv.Hi, iv.Hi), iv.Hi)
		if lo3.Cmp(two) >= 0 || hi3.Cmp(two) <= 0 {
			t.Fatalf("interval (%s,%s) does not provably contain cbrt(2)", iv.Lo, iv.Hi)
		}
	})

	t.Run("x^2-x-1 golden ratio", func(t *testing.T) {
		res, err := Isolate(ints(-1, -1, 1), rat("1"), rat("2"))
		if err != nil {
			t.Fatal(err)
		}
		if res.Count() != 1 {
			t.Fatalf("want exactly one root (golden ratio), got %+v", res)
		}
		iv := res.Intervals[0]
		// phi = (1+sqrt(5))/2, and lo >= 1, so lo < phi  <=>  (2lo-1)^2 < 5.
		five := big.NewRat(5, 1)
		dLo := new(big.Rat).Sub(new(big.Rat).Mul(big.NewRat(2, 1), iv.Lo), big.NewRat(1, 1))
		dHi := new(big.Rat).Sub(new(big.Rat).Mul(big.NewRat(2, 1), iv.Hi), big.NewRat(1, 1))
		if new(big.Rat).Mul(dLo, dLo).Cmp(five) >= 0 ||
			new(big.Rat).Mul(dHi, dHi).Cmp(five) <= 0 {
			t.Fatalf("interval (%s,%s) does not provably contain phi", iv.Lo, iv.Hi)
		}
	})
}

// x^2+1 has no real roots.
func TestNoRealRoots(t *testing.T) {
	res, err := Isolate(ints(1, 0, 1), rat("-10"), rat("10"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Count() != 0 {
		t.Fatalf("x^2+1 must have 0 real roots, got %d", res.Count())
	}
}

// The zero polynomial is rejected outright.
func TestZeroPolynomialRejected(t *testing.T) {
	if _, err := Isolate(ints(0, 0, 0), rat("0"), rat("1")); err == nil {
		t.Fatal("zero polynomial must be rejected")
	}
	if _, err := Isolate([]*big.Int{}, rat("0"), rat("1")); err == nil {
		t.Fatal("empty polynomial must be rejected")
	}
	if _, err := IsolateAll(ints(0, 0)); err == nil {
		t.Fatal("zero polynomial must be rejected by IsolateAll")
	}
}

// Two rational roots 1/3 and 1/3 + 1e-25 are indistinguishable in float64:
// fixed-precision arithmetic literally cannot tell them apart, so it can
// never serve as the final verdict. Exact arithmetic separates them.
func TestCloseRootsDefeatFloat64(t *testing.T) {
	pow := new(big.Int).Exp(big.NewInt(10), big.NewInt(25), nil)
	// factors (3x - 1) and (3*10^25 x - (10^25 + 3))
	f1 := []*big.Int{big.NewInt(-1), big.NewInt(3)}
	f2 := []*big.Int{
		new(big.Int).Neg(new(big.Int).Add(pow, big.NewInt(3))),
		new(big.Int).Mul(big.NewInt(3), pow),
	}
	p := mulPoly(f1, f2)

	// Demonstrate the failure of fixed precision: both roots collapse to the
	// same float64.
	r1f := 1.0 / 3.0
	r2f := 1.0/3.0 + 1e-25
	if r1f != r2f {
		t.Fatal("test assumption broken: float64 should merge the two roots")
	}

	res, err := Isolate(p, rat("0"), rat("1"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Count() != 2 {
		t.Fatalf("exact arithmetic must find 2 distinct roots, got %d", res.Count())
	}
	if len(res.Intervals) != 2 {
		t.Fatalf("want 2 disjoint isolating intervals, got %+v", res)
	}
	checkDisjoint(t, res)
	checkEvidence(t, res)
	if res.Intervals[0].Hi.Cmp(res.Intervals[1].Lo) > 0 {
		t.Fatal("isolating intervals for the twin roots overlap")
	}
}

// IsolateAll must find every real root, including dyadic ones that bisection
// reports as exact points.
func TestIsolateAll(t *testing.T) {
	res, err := IsolateAll(ints(-1, 0, 1)) // x^2 - 1
	if err != nil {
		t.Fatal(err)
	}
	if res.Count() != 2 {
		t.Fatalf("want 2 roots, got %d", res.Count())
	}
	if !covered(res, rat("-1")) || !covered(res, rat("1")) {
		t.Fatalf("roots -1 and 1 must be covered, got %+v", res)
	}
	checkDisjoint(t, res)
	checkEvidence(t, res)
}

// A dyadic rational root hit exactly by bisection is reported as a point,
// and isolation continues correctly on both sides.
func TestDyadicRootHitByBisection(t *testing.T) {
	// roots: -1/2, 0, 3
	p := product(lin(-1, 2), lin(0, 1), lin(3, 1))
	res, err := Isolate(p, rat("-2"), rat("4"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Count() != 3 {
		t.Fatalf("want 3 roots, got %d", res.Count())
	}
	for _, rs := range []string{"-1/2", "0", "3"} {
		if !covered(res, rat(rs)) {
			t.Fatalf("root %s missing from %+v", rs, res)
		}
	}
	checkDisjoint(t, res)
	checkEvidence(t, res)
}
