package polygcd

import (
	"math/big"
	"testing"
)

// mulAll multiplies a list of polynomials.
func mulAll(ps ...Poly) Poly {
	acc := One()
	for _, p := range ps {
		acc = Mul(acc, p)
	}
	return acc
}

// pow computes p^n.
func pow(p Poly, n int) Poly {
	acc := One()
	for i := 0; i < n; i++ {
		acc = Mul(acc, p)
	}
	return acc
}

func xplus(c int64) Poly { return PolyFromInts(c, 1) } // x + c

// TestKnownFactorWithCoprimeParts builds inputs as d*p and d*q with an a
// priori known common factor d and coprime cofactors p, q, then checks the
// GCD equals monic(d) exactly and that the Bezout certificate verifies.
func TestKnownFactorWithCoprimeParts(t *testing.T) {
	d := mulAll(pow(xplus(-1), 2), xplus(2)) // (x-1)^2 (x+2)
	p := PolyFromInts(1, 0, 1)               // x^2+1
	q := PolyFromInts(1, 1, 1)               // x^2+x+1
	a := Mul(d, p).Scale(big.NewRat(3, 4))
	b := Mul(d, q).Scale(big.NewRat(-5, 7))

	cert := ExtendedGCD(a, b)
	if want := d.Monic(); !cert.G.Equal(want) {
		t.Fatalf("G = %v, want %v", cert.G, want)
	}
	if !Verify(a, b, cert) {
		t.Fatalf("certificate failed independent verification")
	}
}

// TestRepeatedRoots uses a common factor with repeated roots of
// multiplicity > 1, including a non-monic linear factor.
func TestRepeatedRoots(t *testing.T) {
	d := mulAll(pow(xplus(-1), 3), pow(xplus(2), 2), PolyFromInts(-3, 2)) // (x-1)^3 (x+2)^2 (2x-3)
	p := PolyFromInts(1, -1, 1)                                           // x^2-x+1
	q := PolyFromInts(1, 1, 1)                                            // x^2+x+1, coprime to p
	a := Mul(d, p).Scale(big.NewRat(2, 9))
	b := Mul(d, q).Scale(big.NewRat(7, 4))

	cert := ExtendedGCD(a, b)
	if want := d.Monic(); !cert.G.Equal(want) {
		t.Fatalf("G = %v, want %v", cert.G, want)
	}
	if !Verify(a, b, cert) {
		t.Fatalf("certificate failed independent verification")
	}
}

// TestCoprimeInputs: no common factor, GCD must be 1 with a Bezout
// certificate for 1.
func TestCoprimeInputs(t *testing.T) {
	a := mulAll(PolyFromInts(1, 0, 1), xplus(-2)) // (x^2+1)(x-2)
	b := mulAll(PolyFromInts(1, 1, 1), xplus(5))  // (x^2+x+1)(x+5)
	cert := ExtendedGCD(a, b)
	if !cert.G.Equal(One()) {
		t.Fatalf("G = %v, want 1", cert.G)
	}
	if !Verify(a, b, cert) {
		t.Fatalf("certificate failed independent verification")
	}
}

// TestZeroPolynomial pins down the zero-polynomial conventions.
func TestZeroPolynomial(t *testing.T) {
	z := Zero()
	// GCD(0,0) = 0, S = T = 0.
	c := ExtendedGCD(z, z)
	if !c.G.IsZero() || !c.S.IsZero() || !c.T.IsZero() {
		t.Fatalf("GCD(0,0) = (%v, %v, %v), want (0,0,0)", c.G, c.S, c.T)
	}
	if !Verify(z, z, c) {
		t.Fatal("zero certificate failed verification")
	}
	// GCD(a,0) = monic(a).
	a := PolyFromRats("3/2", "-1", "0", "2")
	c = ExtendedGCD(a, z)
	if !c.G.Equal(a.Monic()) {
		t.Fatalf("GCD(a,0) = %v, want %v", c.G, a.Monic())
	}
	if !Verify(a, z, c) {
		t.Fatal("GCD(a,0) certificate failed verification")
	}
	// GCD(0,b) = monic(b).
	b := PolyFromInts(4, 0, -2)
	c = ExtendedGCD(z, b)
	if !c.G.Equal(b.Monic()) {
		t.Fatalf("GCD(0,b) = %v, want %v", c.G, b.Monic())
	}
	if !Verify(z, b, c) {
		t.Fatal("GCD(0,b) certificate failed verification")
	}
}

// TestConstants pins down constant-polynomial conventions.
func TestConstants(t *testing.T) {
	// Two nonzero constants: GCD is 1, and 1 = (1/6)*6 + 0*15.
	c := ExtendedGCD(PolyFromInts(6), PolyFromInts(15))
	if !c.G.Equal(One()) {
		t.Fatalf("GCD(6,15) = %v, want 1", c.G)
	}
	if !Verify(PolyFromInts(6), PolyFromInts(15), c) {
		t.Fatal("constant certificate failed verification")
	}
	// Constant and polynomial: GCD is 1.
	p := PolyFromInts(1, 2, 3)
	c = ExtendedGCD(PolyFromRats("-4/5"), p)
	if !c.G.Equal(One()) {
		t.Fatalf("GCD(const,poly) = %v, want 1", c.G)
	}
	if !Verify(PolyFromRats("-4/5"), p, c) {
		t.Fatal("const/poly certificate failed verification")
	}
	// Zero constant and nonzero constant.
	c = ExtendedGCD(Zero(), PolyFromInts(-8))
	if !c.G.Equal(One()) {
		t.Fatalf("GCD(0,-8) = %v, want 1", c.G)
	}
}

// TestBezoutCertificates checks the Bezout identity S*a+T*b = G on a table
// of constructed pairs, all through the independent verifier.
func TestBezoutCertificates(t *testing.T) {
	d1 := mulAll(xplus(-1), xplus(3))
	d2 := pow(PolyFromInts(1, 1), 2) // (x+1)^2
	cases := []struct{ a, b Poly }{
		{Mul(d1, PolyFromInts(2, 0, 1)), Mul(d1, PolyFromInts(-1, 3))},
		{Mul(d2, PolyFromRats("1/2", "0", "0", "1")), Mul(d2, PolyFromRats("0", "-2/3", "1"))},
		{PolyFromRats("1/2", "1/3", "-2/5", "4"), PolyFromRats("-3", "0", "7/2")},
		{pow(xplus(-2), 4), pow(xplus(-2), 2)},   // one divides the other
		{PolyFromInts(0, 1), PolyFromInts(0, 1)}, // identical inputs
	}
	for i, tc := range cases {
		cert := ExtendedGCD(tc.a, tc.b)
		if !Verify(tc.a, tc.b, cert) {
			t.Fatalf("case %d: certificate failed verification (G=%v)", i, cert.G)
		}
		if !cert.G.IsZero() && cert.G.LC().Cmp(big.NewRat(1, 1)) != 0 {
			t.Fatalf("case %d: GCD %v not monic", i, cert.G)
		}
	}
}

// TestVerifyRejectsTamperedCertificate ensures the independent verifier
// actually catches wrong certificates.
func TestVerifyRejectsTamperedCertificate(t *testing.T) {
	d := mulAll(xplus(1), xplus(-4))
	a := Mul(d, PolyFromInts(3, 1))
	b := Mul(d, PolyFromInts(0, 0, 2))
	cert := ExtendedGCD(a, b)
	if !Verify(a, b, cert) {
		t.Fatal("honest certificate rejected")
	}
	bad := cert
	bad.S = Add(cert.S, One())
	if Verify(a, b, bad) {
		t.Fatal("tampered S accepted")
	}
	bad = cert
	bad.G = Add(cert.G, One())
	if Verify(a, b, bad) {
		t.Fatal("tampered G accepted")
	}
}

// naivePRSMaxBits returns the maximum integer coefficient bit length in the
// classical (non-subresultant) pseudo-remainder sequence, which is known to
// grow exponentially.
func naivePRSMaxBits(A, B []*big.Int) int {
	m := 0
	for !iIsZero(B) {
		for _, c := range B {
			m = maxInt(m, c.BitLen())
		}
		_, prem := iPseudoDivRem(A, B)
		A, B = B, prem
	}
	return m
}

// TestBitLengthStats checks that statistics are recorded and that the
// subresultant sequence stays well below the naive PRS coefficient growth.
func TestBitLengthStats(t *testing.T) {
	d := mulAll(xplus(-1), xplus(-2), xplus(-3), xplus(-4))
	p := PolyFromInts(5, 3, 2, 0, 1, 0, -3, 2)      // 2x^7-3x^6+x^4+2x^2+3x+5
	q := PolyFromInts(11, 7, -1, 0, 0, 1, 4, 0, -2) // -2x^8+4x^6+x^5-x^2+7x+11
	a := Mul(d, p).Scale(big.NewRat(3, 8))
	b := Mul(d, q).Scale(big.NewRat(-2, 5))

	cert := ExtendedGCD(a, b)
	if !Verify(a, b, cert) {
		t.Fatal("certificate failed verification")
	}
	st := cert.Stats
	if st.Steps == 0 || st.MaxRemIntBits == 0 || st.ResultNumBits == 0 {
		t.Fatalf("stats not recorded: %+v", st)
	}
	if !cert.G.Equal(d.Monic()) {
		t.Fatalf("G = %v, want %v", cert.G, d.Monic())
	}

	_, A := a.toPrimitiveZ()
	_, B := b.toPrimitiveZ()
	naive := naivePRSMaxBits(A, B)
	t.Logf("subresultant max remainder bits: %d, naive PRS max bits: %d, result bits: %d/%d",
		st.MaxRemIntBits, naive, st.ResultNumBits, st.ResultDenBits)
	if st.MaxRemIntBits > naive {
		t.Fatalf("subresultant growth %d bits exceeds naive PRS %d bits", st.MaxRemIntBits, naive)
	}
}
