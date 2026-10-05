package algnum

import (
	"math/big"
	"testing"
)

func rat(t *testing.T, s string) *big.Rat {
	t.Helper()
	v, ok := new(big.Rat).SetString(s)
	if !ok {
		t.Fatalf("bad rational %q", s)
	}
	return v
}

func intPoly(coeffs ...int64) Poly {
	c := make([]*big.Rat, len(coeffs))
	for i, v := range coeffs {
		c[i] = big.NewRat(v, 1)
	}
	return NewPoly(c...)
}

// lin returns the integer polynomial d*x - n with root n/d.
func lin(n, d int64) Poly { return intPoly(-n, d) }

func num(t *testing.T, p Poly, lo, hi string) Num {
	t.Helper()
	return Num{P: p, Lo: rat(t, lo), Hi: rat(t, hi)}
}

func compareOK(t *testing.T, a, b Num) (Relation, Certificate) {
	t.Helper()
	rel, cert, err := Compare(a, b)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if err := Verify(a, b, cert); err != nil {
		t.Fatalf("Verify(%s) rejected its own certificate: %v", rel, err)
	}
	return rel, cert
}

func TestEqualSameRootDifferentPolynomials(t *testing.T) {
	// sqrt(2) as x^2-2 and as (x^2-2)(x-7): different polynomials, same root.
	a := num(t, intPoly(-2, 0, 1), "1", "3/2")
	b := num(t, Mul(intPoly(-2, 0, 1), lin(7, 1)), "1", "3/2")
	rel, cert := compareOK(t, a, b)
	if rel != Equal {
		t.Fatalf("got %s, want equal", rel)
	}
	if len(cert.GCD) < 2 {
		t.Fatalf("equal certificate missing non-constant gcd: %+v", cert)
	}
}

func TestRepeatedRoots(t *testing.T) {
	// (x-2)^2 (x+3)^3 has a double root at 2; must equal x-2's root.
	p := Mul(Pow(lin(2, 1), 2), Pow(lin(-3, 1), 3))
	a := num(t, p, "3/2", "5/2")
	b := num(t, lin(2, 1), "3/2", "5/2")
	if rel, _ := compareOK(t, a, b); rel != Equal {
		t.Fatalf("got %s, want equal", rel)
	}

	// (x^2-2)^2 vs x^2-2: repeated irrational factor.
	c := num(t, Pow(intPoly(-2, 0, 1), 2), "1", "3/2")
	d := num(t, intPoly(-2, 0, 1), "1", "3/2")
	if rel, _ := compareOK(t, c, d); rel != Equal {
		t.Fatalf("got %s, want equal", rel)
	}

	// Square-free factorization of (x-1)^2 (2x+3)^3 (x-2).
	q := Mul(Mul(Pow(lin(1, 1), 2), Pow(lin(-3, 2), 3)), lin(2, 1))
	factors := SquareFreeFactors(q)
	gotDeg := 0
	recon := Const(big.NewRat(1, 1))
	for _, f := range factors {
		gotDeg += f.F.Deg() * f.Exp
		recon = Mul(recon, Pow(f.F, f.Exp))
		if g := GCD(f.F, f.F.Deriv()); g.Deg() != 0 {
			t.Fatalf("factor %v (exp %d) is not square-free", f.F, f.Exp)
		}
	}
	if gotDeg != q.Deg() {
		t.Fatalf("factor degrees sum to %d, want %d", gotDeg, q.Deg())
	}
	quo, ok := q.QuoExact(recon)
	if !ok || quo.Deg() != 0 {
		t.Fatalf("product of F_i^i does not reconstruct p up to a constant")
	}
}

func TestOverlappingIntervalsDistinctRoots(t *testing.T) {
	// Roots 7/5 = 1.4 and 141/100 = 1.41 with identical, fully
	// overlapping isolating intervals: must not be judged equal.
	a := num(t, lin(7, 5), "1", "2")
	b := num(t, lin(141, 100), "1", "2")
	rel, cert := compareOK(t, a, b)
	if rel != Less {
		t.Fatalf("got %s, want less", rel)
	}
	if rat(t, cert.LhsHi).Cmp(rat(t, cert.RhsLo)) > 0 {
		t.Fatalf("less certificate intervals not disjoint: %+v", cert)
	}
	if rel, _ := compareOK(t, b, a); rel != Greater {
		t.Fatalf("swapped: got %s, want greater", rel)
	}
}

func TestNegativeRoots(t *testing.T) {
	// -sqrt(3) < -sqrt(2), identical intervals (-2, -1).
	a := num(t, intPoly(-3, 0, 1), "-2", "-1")
	b := num(t, intPoly(-2, 0, 1), "-2", "-1")
	if rel, _ := compareOK(t, a, b); rel != Less {
		t.Fatalf("got %s, want less", rel)
	}
	if rel, _ := compareOK(t, b, a); rel != Greater {
		t.Fatalf("swapped: got %s, want greater", rel)
	}
}

func TestRationalRoots(t *testing.T) {
	// 3 < 7/2.
	a := num(t, lin(3, 1), "5/2", "7/2")
	b := num(t, lin(7, 2), "3", "4")
	if rel, _ := compareOK(t, a, b); rel != Less {
		t.Fatalf("got %s, want less", rel)
	}

	// Rational equality through different polynomials: (x-2)(x-9) vs (x-2)(x+7).
	c := num(t, Mul(lin(2, 1), lin(9, 1)), "3/2", "5/2")
	d := num(t, Mul(lin(2, 1), lin(-7, 1)), "3/2", "5/2")
	if rel, _ := compareOK(t, c, d); rel != Equal {
		t.Fatalf("got %s, want equal", rel)
	}

	// Midpoint of the interval is exactly the root (refinement edge case):
	// 1 vs 3/2, both initially isolated in (0, 2).
	e := num(t, lin(1, 1), "0", "2")
	f := num(t, lin(3, 2), "0", "2")
	if rel, _ := compareOK(t, e, f); rel != Less {
		t.Fatalf("got %s, want less", rel)
	}
}

func TestInvalidIntervals(t *testing.T) {
	cases := []struct {
		name string
		n    Num
	}{
		{"two roots in interval", num(t, Mul(lin(1, 1), lin(2, 1)), "0", "3")},
		{"no root in interval", num(t, lin(5, 1), "0", "3")},
		{"lower endpoint is root", num(t, lin(1, 1), "1", "2")},
		{"upper endpoint is root", num(t, lin(2, 1), "1", "2")},
		{"empty interval", num(t, lin(1, 1), "2", "2")},
		{"inverted interval", num(t, lin(1, 1), "3", "2")},
		{"zero polynomial", num(t, Poly{}, "0", "1")},
		{"constant polynomial", num(t, intPoly(5), "0", "1")},
		{"degree too high", num(t, Pow(lin(1, 1), 9), "0", "2")},
	}
	for _, tc := range cases {
		if err := Validate(tc.n); err == nil {
			t.Errorf("%s: Validate accepted invalid input", tc.name)
		}
	}
	// Compare must surface the error, not silently pick a root.
	good := num(t, lin(1, 1), "0", "2")
	bad := num(t, Mul(lin(1, 1), lin(3, 1)), "0", "4") // roots 1 and 3
	if _, _, err := Compare(good, bad); err == nil {
		t.Fatal("Compare accepted an interval with two roots")
	}
	if _, _, err := Compare(bad, good); err == nil {
		t.Fatal("Compare accepted an interval with two roots (lhs)")
	}
}

// TestLinearFactorProducts cross-checks comparisons and root counts against
// known rational roots of an integer polynomial built as a product of
// linear factors.
func TestLinearFactorProducts(t *testing.T) {
	roots := []string{"-5/2", "-1", "0", "1/3", "2", "7/2"}
	factors := []Poly{lin(-5, 2), lin(-1, 1), lin(0, 1), lin(1, 3), lin(2, 1), lin(7, 2)}
	full := Const(big.NewRat(1, 1))
	for _, f := range factors {
		full = Mul(full, f)
	}
	if full.Deg() != len(roots) {
		t.Fatalf("product degree %d, want %d", full.Deg(), len(roots))
	}

	// Independent root counting against the known root set.
	counts := []struct {
		lo, hi string
		want   int
	}{
		{"-3", "4", 6},
		{"-2", "1", 3},      // -1, 0, 1/3
		{"1/2", "3", 1},     // 2
		{"-3", "-2", 1},     // -5/2
		{"2/5", "3/2", 0},   // between 1/3 and 2: no roots
		{"-7/8", "-1/8", 0}, // between -1 and 0: no roots
	}
	st := NewSturmSeq(full.SquareFree())
	for _, c := range counts {
		if got := st.CountRoots(rat(t, c.lo), rat(t, c.hi)); got != c.want {
			t.Errorf("CountRoots(%s, %s) = %d, want %d", c.lo, c.hi, got, c.want)
		}
	}

	// Isolate each root of the full product and compare every pair; the
	// order must match the rational order of the known roots.
	nums := make([]Num, len(roots))
	for i, r := range roots {
		rv := rat(t, r)
		lo := new(big.Rat).Sub(rv, big.NewRat(1, 8))
		hi := new(big.Rat).Add(rv, big.NewRat(1, 8))
		nums[i] = Num{P: full, Lo: lo, Hi: hi}
		if err := Validate(nums[i]); err != nil {
			t.Fatalf("root %s: %v", r, err)
		}
	}
	for i := range nums {
		for j := range nums {
			rel, cert := compareOK(t, nums[i], nums[j])
			want := Equal
			if i < j {
				want = Less
			} else if i > j {
				want = Greater
			}
			if rel != want {
				t.Errorf("Compare(root[%d]=%s, root[%d]=%s) = %s, want %s",
					i, roots[i], j, roots[j], rel, want)
			}
			_ = cert
		}
	}
}

func TestVerifyRejectsForgedCertificates(t *testing.T) {
	a := num(t, lin(7, 5), "1", "2")     // 1.4
	b := num(t, lin(141, 100), "1", "2") // 1.41
	_, cert := compareOK(t, a, b)        // less

	// Flipped relation.
	forged := cert
	forged.Relation = "greater"
	if err := Verify(a, b, forged); err == nil {
		t.Error("accepted certificate with flipped relation")
	}

	// Swapped intervals.
	forged = cert
	forged.LhsLo, forged.RhsLo = cert.RhsLo, cert.LhsLo
	forged.LhsHi, forged.RhsHi = cert.RhsHi, cert.LhsHi
	if err := Verify(a, b, forged); err == nil {
		t.Error("accepted certificate with swapped intervals")
	}

	// Refined interval escaping the original interval.
	forged = cert
	forged.LhsLo = "-10"
	if err := Verify(a, b, forged); err == nil {
		t.Error("accepted refined interval outside the original")
	}

	// Equality claim for distinct numbers.
	eq := Certificate{Relation: "equal", GCD: []string{"-7/5", "1"}, CommonLo: "1", CommonHi: "2"}
	if err := Verify(a, b, eq); err == nil {
		t.Error("accepted equality of 7/5 and 141/100")
	}

	// Gcd that does not divide both polynomials.
	c := num(t, intPoly(-2, 0, 1), "1", "3/2")
	d := num(t, Mul(intPoly(-2, 0, 1), lin(7, 1)), "1", "3/2")
	_, eqCert := compareOK(t, c, d)
	badGcd := eqCert
	badGcd.GCD = []string{"-3", "1"} // x-3 divides neither
	if err := Verify(c, d, badGcd); err == nil {
		t.Error("accepted non-divisor gcd")
	}

	// Common interval moved off the shared root.
	moved := eqCert
	moved.CommonLo, moved.CommonHi = "5/4", "3/2" // sqrt(2) ~ 1.4142 not in (1.25, 1.5)? it is; use (1.41,1.5) shift below
	moved.CommonLo, moved.CommonHi = "1", "7/5"   // sqrt(2) > 7/5, so no gcd root inside
	if err := Verify(c, d, moved); err == nil {
		t.Error("accepted common interval without the shared root")
	}

	// Certificate for invalid input must fail at input validation.
	bad := num(t, lin(5, 1), "0", "3") // no root in (0,3)
	if err := Verify(bad, d, eqCert); err == nil {
		t.Error("accepted certificate for invalid input")
	}
}

func TestJSONRoundTrip(t *testing.T) {
	req := Request{
		Lhs: NumJSON{Coeffs: []string{"-2", "0", "1"}, Lo: "1", Hi: "3/2"},
		Rhs: NumJSON{Coeffs: []string{"-3", "0", "1"}, Lo: "1", Hi: "2"},
	}
	a, b, err := req.Parse()
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	rel, cert, err := Compare(a, b)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if rel != Less { // sqrt(2) < sqrt(3)
		t.Fatalf("got %s, want less", rel)
	}
	if err := Verify(a, b, cert); err != nil {
		t.Fatalf("Verify: %v", err)
	}

	// Non-integer coefficients must be rejected on the wire.
	bad := Request{
		Lhs: NumJSON{Coeffs: []string{"-1/2", "1"}, Lo: "0", Hi: "1"},
		Rhs: NumJSON{Coeffs: []string{"-1", "1"}, Lo: "0", Hi: "2"},
	}
	if _, _, err := bad.Parse(); err == nil {
		t.Fatal("accepted non-integer coefficient")
	}
}
