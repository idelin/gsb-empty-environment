package main

import (
	"math/big"
	"testing"
)

func rat(t *testing.T, s string) *big.Rat {
	t.Helper()
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		t.Fatalf("bad rational %q", s)
	}
	return r
}

// linearFactor returns (den*x - num), i.e. the root num/den.
func linearFactor(num, den int64) Poly {
	return PolyFromInts(-num, den)
}

func mustAnalyze(t *testing.T, p Poly, a, b string) *Report {
	t.Helper()
	rep, err := Analyze(p, rat(t, a), rat(t, b))
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}
	return rep
}

// checkInvariants verifies the contract for every report: intervals are
// pairwise disjoint, lie inside (a, b), and carry valid sign-change evidence.
func checkInvariants(t *testing.T, rep *Report) {
	t.Helper()
	var prevUpper *big.Rat
	for i, ev := range rep.Roots {
		if ev.Exact != nil {
			if rep.SquareFree.Eval(ev.Exact).Sign() != 0 {
				t.Errorf("root %d: %s claimed exact but is not a root", i, ev.Exact)
			}
			if ev.Exact.Cmp(rep.A) <= 0 || ev.Exact.Cmp(rep.B) >= 0 {
				t.Errorf("root %d: exact root %s outside open interval", i, ev.Exact)
			}
			if prevUpper != nil && ev.Exact.Cmp(prevUpper) <= 0 {
				t.Errorf("root %d: overlaps previous interval", i)
			}
			prevUpper = ev.Exact
			continue
		}
		if ev.Lower.Cmp(ev.Upper) >= 0 {
			t.Errorf("root %d: degenerate interval (%s, %s)", i, ev.Lower, ev.Upper)
		}
		if ev.Lower.Cmp(rep.A) < 0 || ev.Upper.Cmp(rep.B) > 0 {
			t.Errorf("root %d: interval (%s, %s) escapes (%s, %s)", i, ev.Lower, ev.Upper, rep.A, rep.B)
		}
		if prevUpper != nil && ev.Lower.Cmp(prevUpper) < 0 {
			t.Errorf("root %d: interval (%s, %s) overlaps previous interval ending %s",
				i, ev.Lower, ev.Upper, prevUpper)
		}
		if ev.LowerVal.Sign() == 0 || ev.UpperVal.Sign() == 0 ||
			ev.LowerVal.Sign() == ev.UpperVal.Sign() {
			t.Errorf("root %d: no sign change: q(%s)=%s, q(%s)=%s",
				i, ev.Lower, ev.LowerVal, ev.Upper, ev.UpperVal)
		}
		prevUpper = ev.Upper
	}
}

// containsRoot reports whether some isolation in rep contains the given root.
func containsRoot(rep *Report, root *big.Rat) bool {
	for _, ev := range rep.Roots {
		if ev.Exact != nil {
			if ev.Exact.Cmp(root) == 0 {
				return true
			}
			continue
		}
		if ev.Lower.Cmp(root) < 0 && root.Cmp(ev.Upper) < 0 {
			return true
		}
	}
	return false
}

// TestRationalRootsProduct multiplies out (x-1)(2x-1)(x+3)(x-2) and checks
// that every known rational root is isolated exactly once.
func TestRationalRootsProduct(t *testing.T) {
	p := PolyFromInts(1) // p = 1
	for _, f := range [][2]int64{{1, 1}, {1, 2}, {-3, 1}, {2, 1}} {
		p = p.Mul(linearFactor(f[0], f[1]))
	}
	// p = 2x^4 - 5x^3 - 9x^2 + 22x - 6, roots: 1, 1/2, -3, 2
	rep := mustAnalyze(t, p, "-4", "3")
	checkInvariants(t, rep)
	if rep.Count != 4 {
		t.Fatalf("want 4 distinct roots, got %d", rep.Count)
	}
	for _, s := range []string{"1", "1/2", "-3", "2"} {
		if !containsRoot(rep, rat(t, s)) {
			t.Errorf("known root %s not isolated", s)
		}
	}
	if rep.RootAtA || rep.RootAtB {
		t.Errorf("unexpected endpoint root report")
	}
}

// TestIrrationalSqrt2 isolates sqrt(2) and proves containment exactly:
// the isolating interval (l, r) satisfies l^2 < 2 < r^2 over the rationals.
func TestIrrationalSqrt2(t *testing.T) {
	p := PolyFromInts(-2, 0, 1) // x^2 - 2
	rep := mustAnalyze(t, p, "0", "2")
	checkInvariants(t, rep)
	if rep.Count != 1 {
		t.Fatalf("want 1 root, got %d", rep.Count)
	}
	ev := rep.Roots[0]
	if ev.Exact != nil {
		t.Fatalf("sqrt(2) is irrational; got exact root %s", ev.Exact)
	}
	two := big.NewRat(2, 1)
	l2 := new(big.Rat).Mul(ev.Lower, ev.Lower)
	r2 := new(big.Rat).Mul(ev.Upper, ev.Upper)
	if l2.Cmp(two) >= 0 || r2.Cmp(two) <= 0 {
		t.Errorf("interval (%s, %s) does not provably contain sqrt(2)", ev.Lower, ev.Upper)
	}
	if ev.LowerVal.Sign() >= 0 || ev.UpperVal.Sign() <= 0 {
		t.Errorf("want q(l)<0<q(r), got %s and %s", ev.LowerVal, ev.UpperVal)
	}
}

// TestGoldenRatio isolates (1+sqrt(5))/2 from x^2 - x - 1.
func TestGoldenRatio(t *testing.T) {
	p := PolyFromInts(-1, -1, 1) // x^2 - x - 1
	rep := mustAnalyze(t, p, "0", "2")
	checkInvariants(t, rep)
	if rep.Count != 1 {
		t.Fatalf("want 1 root, got %d", rep.Count)
	}
	ev := rep.Roots[0]
	// phi in (l, r)  <=>  l^2 - l - 1 < 0 < r^2 - r - 1 (parabola opens up)
	if ev.LowerVal.Sign() >= 0 || ev.UpperVal.Sign() <= 0 {
		t.Errorf("bad evidence signs: %s, %s", ev.LowerVal, ev.UpperVal)
	}
}

// TestMultiplicityDedup: (x-1)^2 (x-2) has only 2 distinct roots.
func TestMultiplicityDedup(t *testing.T) {
	f := linearFactor(1, 1)
	p := f.Mul(f).Mul(linearFactor(2, 1)) // (x-1)^2 (x-2)
	rep := mustAnalyze(t, p, "0", "3")
	checkInvariants(t, rep)
	if rep.Count != 2 {
		t.Fatalf("want 2 distinct roots, got %d", rep.Count)
	}
	if rep.SquareFree.Degree() != 2 {
		t.Errorf("square-free part should have degree 2, got %d", rep.SquareFree.Degree())
	}
	for _, s := range []string{"1", "2"} {
		if !containsRoot(rep, rat(t, s)) {
			t.Errorf("root %s not isolated", s)
		}
	}
}

// TestZeroPolynomialRejected ensures the zero polynomial is an error.
func TestZeroPolynomialRejected(t *testing.T) {
	if _, err := Analyze(Poly{}, big.NewRat(0, 1), big.NewRat(1, 1)); err == nil {
		t.Fatal("zero polynomial must be rejected")
	}
	if _, err := Analyze(NewPoly(big.NewRat(0, 1), big.NewRat(0, 1)), big.NewRat(0, 1), big.NewRat(1, 1)); err == nil {
		t.Fatal("polynomial with all-zero coefficients must be rejected")
	}
}

// TestEndpointRoots: x(x-1)(x+1) on (-1, 1): both endpoints are roots and
// must be reported separately; the only interior root is 0.
func TestEndpointRoots(t *testing.T) {
	p := PolyFromInts(0, 1).Mul(linearFactor(1, 1)).Mul(linearFactor(-1, 1))
	rep := mustAnalyze(t, p, "-1", "1")
	checkInvariants(t, rep)
	if !rep.RootAtA || !rep.RootAtB {
		t.Errorf("endpoints -1 and 1 are roots and must be reported: got %v, %v", rep.RootAtA, rep.RootAtB)
	}
	if rep.Count != 1 {
		t.Fatalf("want 1 interior root, got %d", rep.Count)
	}
	if !containsRoot(rep, big.NewRat(0, 1)) {
		t.Error("interior root 0 not isolated")
	}
}

// TestCloseRootsBeyondFloat64 uses two roots 1/3 and 1/3 + 10^-25. Their
// separation is far below float64 epsilon at that magnitude (~2.2e-16), so
// any fixed-precision sign evaluation cannot distinguish them; exact rational
// arithmetic must still count and isolate both.
func TestCloseRootsBeyondFloat64(t *testing.T) {
	// p = (3x - 1)(3*10^25 x - (10^25 + 3))
	d := new(big.Int).Exp(big.NewInt(10), big.NewInt(25), nil)
	f1 := NewPoly(big.NewRat(-1, 1), big.NewRat(3, 1))
	c0 := new(big.Int).Add(d, big.NewInt(3))
	c0.Neg(c0)
	c1 := new(big.Int).Mul(d, big.NewInt(3))
	f2 := NewPoly(new(big.Rat).SetInt(c0), new(big.Rat).SetInt(c1))
	p := f1.Mul(f2)

	rep := mustAnalyze(t, p, "0", "1")
	checkInvariants(t, rep)
	if rep.Count != 2 {
		t.Fatalf("want 2 distinct roots separated by 1e-25, got %d", rep.Count)
	}
	r1 := rat(t, "1/3")
	r2 := new(big.Rat).Add(r1, rat(t, "1/10000000000000000000000000"))
	if !containsRoot(rep, r1) || !containsRoot(rep, r2) {
		t.Errorf("roots 1/3 and 1/3+1e-25 not both isolated")
	}
}

// TestNoRealRoots: x^2 + 1 has no real roots.
func TestNoRealRoots(t *testing.T) {
	rep := mustAnalyze(t, PolyFromInts(1, 0, 1), "-10", "10")
	checkInvariants(t, rep)
	if rep.Count != 0 {
		t.Fatalf("want 0 roots, got %d", rep.Count)
	}
}

// TestMixedRationalAndIrrational: (x^2-2)(x-5) on (0, 10) has roots
// sqrt(2) and 5; on (0, 5) only sqrt(2), with 5 excluded as an endpoint root.
func TestMixedRationalAndIrrational(t *testing.T) {
	p := PolyFromInts(-2, 0, 1).Mul(linearFactor(5, 1))
	rep := mustAnalyze(t, p, "0", "10")
	checkInvariants(t, rep)
	if rep.Count != 2 {
		t.Fatalf("want 2 roots, got %d", rep.Count)
	}
	if !containsRoot(rep, big.NewRat(5, 1)) {
		t.Error("rational root 5 not isolated")
	}

	rep2 := mustAnalyze(t, p, "0", "5")
	checkInvariants(t, rep2)
	if rep2.Count != 1 || !rep2.RootAtB {
		t.Fatalf("on (0,5): want 1 interior root and endpoint root at 5, got count=%d rootAtB=%v",
			rep2.Count, rep2.RootAtB)
	}
}
