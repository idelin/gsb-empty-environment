package algcmp

import (
	"encoding/json"
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

func mustAlg(t *testing.T, p Poly, l, r string) AlgNum {
	t.Helper()
	a, err := NewAlgNum(p, rat(t, l), rat(t, r))
	if err != nil {
		t.Fatalf("NewAlgNum(%s, %s, %s): %v", p.String(), l, r, err)
	}
	return a
}

func mustCompare(t *testing.T, a, b AlgNum, want int) *CertJSON {
	t.Helper()
	got, cert := Compare(a, b)
	if got != want {
		t.Fatalf("Compare = %d, want %d", got, want)
	}
	return cert
}

func mustVerify(t *testing.T, left, right AlgJSON, cert *CertJSON) {
	t.Helper()
	if err := Verify(Doc{Left: left, Right: right, Certificate: cert}); err != nil {
		t.Fatalf("Verify rejected a valid certificate: %v", err)
	}
}

func aj(p Poly, l, r string) AlgJSON {
	return AlgJSON{Poly: p.IntCoeffs(), Lower: l, Upper: r}
}

// Same real root defined by two different polynomials.
func TestEqualSameRootDifferentPolynomials(t *testing.T) {
	sqrt2a := PolyFromInts(-2, 0, 1)      // x^2 - 2
	sqrt2b := PolyFromInts(10, -2, -5, 1) // (x^2-2)(x-5) = x^3-5x^2-2x+10
	a := mustAlg(t, sqrt2a, "1", "2")
	b := mustAlg(t, sqrt2b, "7/5", "3/2")
	cert := mustCompare(t, a, b, 0)
	mustVerify(t, aj(sqrt2a, "1", "2"), aj(sqrt2b, "7/5", "3/2"), cert)
}

// Polynomials with repeated factors must be handled via square-free
// decomposition.
func TestRepeatedRoots(t *testing.T) {
	p1 := PolyFromInts(4, 0, -3, 1) // (x-2)^2 (x+1) = x^3-3x^2+4
	p2 := PolyFromInts(-2, 1)       // x-2
	a := mustAlg(t, p1, "3/2", "5/2")
	b := mustAlg(t, p2, "3/2", "5/2")
	cert := mustCompare(t, a, b, 0)
	mustVerify(t, aj(p1, "3/2", "5/2"), aj(p2, "3/2", "5/2"), cert)

	// The simple root -1 of p1 against (x+1)^3.
	p3 := PolyFromInts(1, 3, 3, 1) // (x+1)^3
	c := mustAlg(t, p1, "-3/2", "-1/2")
	d := mustAlg(t, p3, "-2", "0")
	cert = mustCompare(t, c, d, 0)
	mustVerify(t, aj(p1, "-3/2", "-1/2"), aj(p3, "-2", "0"), cert)
}

// The two isolating intervals overlap, but the roots are different and
// very close: sqrt(2) = 1.41421356... versus the rational 1.41421.
// Intersecting intervals must NOT be taken as evidence of equality.
func TestOverlappingIntervalsDistinctRoots(t *testing.T) {
	sqrt2 := PolyFromInts(-2, 0, 1)
	near := PolyFromInts(-141421, 100000) // root 1.41421
	a := mustAlg(t, sqrt2, "1", "3/2")
	b := mustAlg(t, near, "7/5", "3/2") // overlaps (1, 3/2) heavily
	cert := mustCompare(t, a, b, 1)     // sqrt(2) > 1.41421
	mustVerify(t, aj(sqrt2, "1", "3/2"), aj(near, "7/5", "3/2"), cert)
	// The certified intervals must be disjoint: right entirely below left.
	if rat(t, cert.Right.Upper).Cmp(rat(t, cert.Left.Lower)) > 0 {
		t.Fatalf("order certificate intervals still overlap: %+v", cert)
	}
	// And the reverse comparison.
	mustCompare(t, b, a, -1)
}

// Negative irrational roots: -sqrt(3) < -sqrt(2).
func TestNegativeRoots(t *testing.T) {
	negSqrt3 := PolyFromInts(-3, 0, 1)
	negSqrt2 := PolyFromInts(-2, 0, 1)
	a := mustAlg(t, negSqrt3, "-2", "-3/2")
	b := mustAlg(t, negSqrt2, "-3/2", "-1")
	cert := mustCompare(t, a, b, -1)
	mustVerify(t, aj(negSqrt3, "-2", "-3/2"), aj(negSqrt2, "-3/2", "-1"), cert)
}

// Rational roots compared through identical, fully overlapping intervals.
func TestRationalRoots(t *testing.T) {
	twoThirds := PolyFromInts(-2, 3)    // 3x-2
	threeFourths := PolyFromInts(-3, 4) // 4x-3
	a := mustAlg(t, twoThirds, "0", "1")
	b := mustAlg(t, threeFourths, "0", "1")
	cert := mustCompare(t, a, b, -1)
	mustVerify(t, aj(twoThirds, "0", "1"), aj(threeFourths, "0", "1"), cert)
}

// Products of rational linear factors: roots are known exactly, so both
// the comparisons and the root counts can be checked independently.
func TestRationalLinearFactorProducts(t *testing.T) {
	// roots: -7/4, 1/2, 5/3
	f1 := PolyFromInts(7, 4)  // 4x+7
	f2 := PolyFromInts(-1, 2) // 2x-1
	f3 := PolyFromInts(-5, 3) // 3x-5
	p := f1.Mul(f2).Mul(f3)

	sf := SquareFree(p)
	if got := CountRoots(sf, rat(t, "-3"), rat(t, "3")); got != 3 {
		t.Fatalf("CountRoots(product, -3, 3) = %d, want 3", got)
	}
	if got := CountRoots(sf, rat(t, "0"), rat(t, "1")); got != 1 {
		t.Fatalf("CountRoots(product, 0, 1) = %d, want 1", got)
	}

	// Isolate each root of the product and compare against the
	// corresponding linear factor: all equal.
	cases := []struct {
		rootAlg  AlgNum
		factor   Poly
		lo, hi   string
		flo, fhi string
	}{
		{mustAlg(t, p, "-2", "-1"), f1, "-2", "-1", "-2", "-1"},
		{mustAlg(t, p, "0", "1"), f2, "0", "1", "0", "1"},
		{mustAlg(t, p, "1", "2"), f3, "1", "2", "1", "2"},
	}
	for i, c := range cases {
		ref := mustAlg(t, c.factor, c.flo, c.fhi)
		cert := mustCompare(t, c.rootAlg, ref, 0)
		mustVerify(t, aj(p, c.lo, c.hi), aj(c.factor, c.flo, c.fhi), cert)
		_ = i
	}

	// Order among the three roots of the product: -7/4 < 1/2 < 5/3.
	r1 := mustAlg(t, p, "-2", "-1")
	r2 := mustAlg(t, p, "0", "1")
	r3 := mustAlg(t, p, "1", "2")
	mustCompare(t, r1, r2, -1)
	mustCompare(t, r2, r3, -1)
	mustCompare(t, r1, r3, -1)
	mustCompare(t, r3, r1, 1)
}

// Bisection landing exactly on a rational root must be handled exactly.
func TestRefineHitsExactRationalRoot(t *testing.T) {
	sqrt2 := PolyFromInts(-2, 0, 1)
	threeHalves := PolyFromInts(-3, 2) // root 3/2
	a := mustAlg(t, sqrt2, "1", "2")
	b := mustAlg(t, threeHalves, "1", "2")
	cert := mustCompare(t, a, b, -1) // sqrt(2) < 3/2
	mustVerify(t, aj(sqrt2, "1", "2"), aj(threeHalves, "1", "2"), cert)
}

// Maximum supported degree: (x^2-2)^4 has degree 8 and sqrt(2) as a
// fourfold root; square-free decomposition must reduce it exactly.
func TestDegreeEightRepeatedRoot(t *testing.T) {
	base := PolyFromInts(-2, 0, 1)
	p := base.Mul(base).Mul(base).Mul(base)
	if p.Deg() != 8 {
		t.Fatalf("degree = %d, want 8", p.Deg())
	}
	a := mustAlg(t, p, "1", "2")
	b := mustAlg(t, base, "7/5", "3/2")
	cert := mustCompare(t, a, b, 0)
	mustVerify(t, aj(p, "1", "2"), aj(base, "7/5", "3/2"), cert)

	// And against a nearby but distinct irrational root of
	// 1000x^2 - 2001 (root ~1.4145660 > sqrt(2) ~1.4142136).
	near := PolyFromInts(-2001, 0, 1000)
	c := mustAlg(t, near, "1", "2")
	cert = mustCompare(t, a, c, -1)
	mustVerify(t, aj(p, "1", "2"), aj(near, "1", "2"), cert)
}

// Illegal isolating intervals must produce explicit errors.
func TestInvalidIsolatingIntervals(t *testing.T) {
	cases := []struct {
		name string
		p    Poly
		l, r string
	}{
		{"zero polynomial", PolyFromInts(), "0", "1"},
		{"empty interval", PolyFromInts(-2, 0, 1), "3/2", "1"},
		{"degenerate interval", PolyFromInts(-2, 0, 1), "1", "1"},
		{"lower endpoint is a root", PolyFromInts(-1, 1), "1", "2"},
		{"upper endpoint is a root", PolyFromInts(-2, 1), "0", "2"},
		{"two roots inside", PolyFromInts(2, -3, 1), "0", "3"}, // (x-1)(x-2)
		{"no root inside", PolyFromInts(-2, 0, 1), "2", "3"},
	}
	for _, c := range cases {
		if _, err := NewAlgNum(c.p, rat(t, c.l), rat(t, c.r)); err == nil {
			t.Errorf("%s: expected error, got none", c.name)
		}
	}
	// Non-integer coefficients are rejected at parse time.
	if _, err := ParsePoly([]string{"1/2", "1"}); err == nil {
		t.Errorf("expected error for non-integer coefficient")
	}
}

// Certificates must be independently verifiable, and tampered
// certificates must be rejected.
func TestCertificateVerification(t *testing.T) {
	sqrt2 := PolyFromInts(-2, 0, 1)
	near := PolyFromInts(-141421, 100000)
	left, right := aj(sqrt2, "1", "3/2"), aj(near, "7/5", "3/2")
	a := mustAlg(t, sqrt2, "1", "3/2")
	b := mustAlg(t, near, "7/5", "3/2")
	_, cert := Compare(a, b)
	mustVerify(t, left, right, cert)

	// Flipping the result must fail.
	bad := *cert
	bad.Result = "less"
	if err := Verify(Doc{Left: left, Right: right, Certificate: &bad}); err == nil {
		t.Errorf("tampered result accepted")
	}

	// Widening a certified interval beyond the input interval must fail.
	bad2 := *cert
	bad2.Left = IntervalJSON{Lower: "0", Upper: "3/2"}
	if err := Verify(Doc{Left: left, Right: right, Certificate: &bad2}); err == nil {
		t.Errorf("out-of-range certificate interval accepted")
	}

	// Overlapping certificate intervals must fail an order claim.
	bad3 := *cert
	bad3.Left = IntervalJSON{Lower: cert.Left.Lower, Upper: "3/2"}
	bad3.Right = IntervalJSON{Lower: "7/5", Upper: "3/2"}
	if err := Verify(Doc{Left: left, Right: right, Certificate: &bad3}); err == nil {
		t.Errorf("overlapping certificate intervals accepted for an order claim")
	}
}

// Equality certificates: wrong gcd or empty meet must be rejected.
func TestEqualityCertificateTampering(t *testing.T) {
	sqrt2a := PolyFromInts(-2, 0, 1)
	sqrt2b := PolyFromInts(10, -2, -5, 1)
	left, right := aj(sqrt2a, "1", "2"), aj(sqrt2b, "7/5", "3/2")
	a := mustAlg(t, sqrt2a, "1", "2")
	b := mustAlg(t, sqrt2b, "7/5", "3/2")
	_, cert := Compare(a, b)
	if cert.Result != "equal" {
		t.Fatalf("expected equality, got %s", cert.Result)
	}
	mustVerify(t, left, right, cert)

	// gcd that does not divide both polynomials.
	bad := *cert
	bad.GCD = []string{"-1", "1"} // x-1
	if err := Verify(Doc{Left: left, Right: right, Certificate: &bad}); err == nil {
		t.Errorf("non-divisor gcd accepted")
	}

	// meet interval without a root of the gcd.
	bad2 := *cert
	bad2.Meet = &IntervalJSON{Lower: "3/2", Upper: "2"}
	if err := Verify(Doc{Left: left, Right: right, Certificate: &bad2}); err == nil {
		t.Errorf("rootless meet interval accepted")
	}
}

// End-to-end JSON round trip through the CLI document format.
func TestJSONRoundTrip(t *testing.T) {
	docJSON := `{
	  "left":  {"poly": ["-2", "0", "1"], "lower": "1", "upper": "2"},
	  "right": {"poly": ["-3", "0", "1"], "lower": "3/2", "upper": "2"}
	}`
	var doc Doc
	if err := json.Unmarshal([]byte(docJSON), &doc); err != nil {
		t.Fatal(err)
	}
	result, cert, err := CompareJSON(doc.Left, doc.Right)
	if err != nil {
		t.Fatal(err)
	}
	if result != "less" {
		t.Fatalf("sqrt(2) vs sqrt(3): got %s, want less", result)
	}
	doc.Certificate = cert
	if err := Verify(doc); err != nil {
		t.Fatalf("round-trip verification failed: %v", err)
	}

	// Invalid input must surface an explicit error, not a guess.
	bad := Doc{}
	if err := json.Unmarshal([]byte(`{
	  "left":  {"poly": ["2", "-3", "1"], "lower": "0", "upper": "3"},
	  "right": {"poly": ["-2", "0", "1"], "lower": "1", "upper": "2"}
	}`), &bad); err != nil {
		t.Fatal(err)
	}
	if _, _, err := CompareJSON(bad.Left, bad.Right); err == nil {
		t.Fatalf("interval with two roots accepted")
	}
}
