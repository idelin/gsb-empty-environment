package ratpoly

import (
	"math/big"
	"math/rand"
	"testing"
)

func pow(p Poly, n int) Poly {
	r := PolyFromInts(1)
	for i := 0; i < n; i++ {
		r = Mul(r, p)
	}
	return r
}

func mustDiv(t *testing.T, a, b Poly) Poly {
	t.Helper()
	q, r, err := DivMod(a, b)
	if err != nil {
		t.Fatalf("DivMod: %v", err)
	}
	if !r.IsZero() {
		t.Fatalf("expected exact division of %v by %v, remainder %v", a, b, r)
	}
	return q
}

func TestZeroAndConstantConventions(t *testing.T) {
	// gcd(0,0) = 0 with zero Bézout coefficients.
	g, u, v, _, err := ExtendedGCD(Zero, Zero)
	if err != nil {
		t.Fatal(err)
	}
	if !g.IsZero() || !u.IsZero() || !v.IsZero() {
		t.Fatalf("gcd(0,0): got g=%v u=%v v=%v, want all zero", g, u, v)
	}

	// gcd(0,f) = gcd(f,0) = Monic(f).
	f := PolyFromInts(2, 4, 6) // 6x^2+4x+2
	want := f.Monic()
	for _, pair := range [][2]Poly{{Zero, f}, {f, Zero}} {
		g, u, v, st, err := ExtendedGCD(pair[0], pair[1])
		if err != nil {
			t.Fatal(err)
		}
		if !g.Equal(want) {
			t.Fatalf("gcd(0,f) = %v, want %v", g, want)
		}
		if !st.Verified {
			t.Fatal("certificate not verified")
		}
		lhs := Add(Mul(u, pair[0]), Mul(v, pair[1]))
		if !lhs.Equal(g) {
			t.Fatalf("Bézout identity failed: %v != %v", lhs, g)
		}
	}

	// Constants: gcd(6,10) = 1 (monic), Bézout identity holds.
	g, u, v, _, err = ExtendedGCD(PolyFromInts(6), PolyFromInts(10))
	if err != nil {
		t.Fatal(err)
	}
	if !g.Equal(PolyFromInts(1)) {
		t.Fatalf("gcd(6,10) = %v, want 1", g)
	}
	if lhs := Add(Mul(u, PolyFromInts(6)), Mul(v, PolyFromInts(10))); !lhs.Equal(g) {
		t.Fatalf("Bézout for constants failed: %v != 1", lhs)
	}

	// gcd(0, c) for constant c is 1.
	if g := GCD(Zero, PolyFromInts(-7)); !g.Equal(PolyFromInts(1)) {
		t.Fatalf("gcd(0,-7) = %v, want 1", g)
	}

	// Constant against nonconstant: gcd is 1.
	g, _, _, st, err := ExtendedGCD(PolyFromInts(4), PolyFromInts(1, 1))
	if err != nil {
		t.Fatal(err)
	}
	if !g.Equal(PolyFromInts(1)) || !st.Verified {
		t.Fatalf("gcd(4, x+1) = %v verified=%v, want 1 true", g, st.Verified)
	}
}

func TestRepeatedRoots(t *testing.T) {
	xm1 := PolyFromInts(-1, 1) // x-1
	xp2 := PolyFromInts(2, 1)  // x+2
	xm3 := PolyFromInts(-3, 1) // x-3
	a := Mul(pow(xm1, 3), pow(xp2, 2))
	b := Mul(pow(xm1, 2), pow(xm3, 4))

	g, u, v, st, err := ExtendedGCD(a, b)
	if err != nil {
		t.Fatal(err)
	}
	want := pow(xm1, 2) // min multiplicity, already monic
	if !g.Equal(want) {
		t.Fatalf("gcd = %v, want %v", g, want)
	}
	if !st.Verified {
		t.Fatal("certificate not verified")
	}
	// Symbolic identity check (no numeric sampling).
	if lhs := Add(Mul(u, a), Mul(v, b)); !lhs.Equal(g) {
		t.Fatalf("Bézout identity failed")
	}
	// Quotients must be exactly the coprime parts.
	if q := mustDiv(t, a, g); !q.Equal(Mul(xm1, pow(xp2, 2))) {
		t.Fatalf("a/g = %v", q)
	}
	if q := mustDiv(t, b, g); !q.Equal(pow(xm3, 4)) {
		t.Fatalf("b/g = %v", q)
	}
}

// randIntPoly builds a degree-d polynomial with small random integer
// coefficients and nonzero leading coefficient.
func randIntPoly(rng *rand.Rand, d, bound int) Poly {
	c := make([]*big.Rat, d+1)
	for i := range c {
		c[i] = big.NewRat(int64(rng.Intn(2*bound+1)-bound), 1)
	}
	for c[d].Sign() == 0 {
		c[d] = big.NewRat(int64(rng.Intn(bound)+1), 1)
	}
	return NewPoly(c...)
}

// TestKnownFactorConstruction builds A = G*P, B = G*Q with a known factor G
// and provably coprime P, Q, then checks gcd(A,B) == Monic(G) symbolically:
// Bézout identity, exact quotients, and coprimality of the cofactors.
func TestKnownFactorConstruction(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 20; trial++ {
		G := randIntPoly(rng, 3, 5)
		P := randIntPoly(rng, 4, 5)
		Q := randIntPoly(rng, 5, 5)
		// Ensure the parts are coprime so the gcd is exactly Monic(G).
		if !GCD(P, Q).Equal(PolyFromInts(1)) {
			continue
		}
		A := Mul(G, P)
		B := Mul(G, Q)

		g, u, v, st, err := ExtendedGCD(A, B)
		if err != nil {
			t.Fatal(err)
		}
		if want := G.Monic(); !g.Equal(want) {
			t.Fatalf("trial %d: gcd = %v, want %v", trial, g, want)
		}
		if !st.Verified {
			t.Fatalf("trial %d: certificate failed independent verification", trial)
		}
		// The Bézout identity must hold symbolically.
		if lhs := Add(Mul(u, A), Mul(v, B)); !lhs.Equal(g) {
			t.Fatalf("trial %d: Bézout identity failed", trial)
		}
		// Quotients recover the coprime parts (up to the unit lc(G)).
		if q := mustDiv(t, A, g); !q.Monic().Equal(P.Monic()) {
			t.Fatalf("trial %d: A/g != Monic(P)", trial)
		}
		if q := mustDiv(t, B, g); !q.Monic().Equal(Q.Monic()) {
			t.Fatalf("trial %d: B/g != Monic(Q)", trial)
		}
		// The recovered parts are coprime.
		if !GCD(mustDiv(t, A, g), mustDiv(t, B, g)).Equal(PolyFromInts(1)) {
			t.Fatalf("trial %d: cofactors not coprime", trial)
		}
	}
}

// naiveExtEuclid is the textbook extended Euclidean algorithm over Q,
// used to demonstrate the coefficient blowup the subresultant PRS avoids.
func naiveExtEuclid(a, b Poly) (g, u, v Poly, maxBits int) {
	note := func(p Poly) {
		if n := BitLen(p); n > maxBits {
			maxBits = n
		}
	}
	r0, r1 := a, b
	s0, s1 := PolyFromInts(1), Zero
	t0, t1 := Zero, PolyFromInts(1)
	for !r1.IsZero() {
		q, r, err := DivMod(r0, r1)
		if err != nil {
			panic(err)
		}
		r0, r1 = r1, r
		s0, s1 = s1, Sub(s0, Mul(q, s1))
		t0, t1 = t1, Sub(t0, Mul(q, t1))
		note(r0)
		note(s0)
		note(t0)
	}
	inv := new(big.Rat).Inv(r0.LC())
	return r0.Monic(), Scale(s0, inv), Scale(t0, inv), maxBits
}

func TestCoefficientBlowupControl(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	G := randIntPoly(rng, 4, 9)
	P := randIntPoly(rng, 6, 9)
	Q := randIntPoly(rng, 6, 9)
	if !GCD(P, Q).Equal(PolyFromInts(1)) {
		t.Skip("parts not coprime, rerun with another seed")
	}
	A := Mul(G, P)
	B := Mul(G, Q)

	_, _, _, st, err := ExtendedGCD(A, B)
	if err != nil {
		t.Fatal(err)
	}
	g2, u2, v2, naiveBits := naiveExtEuclid(A, B)

	t.Logf("subresultant: steps=%d maxRemBits=%d maxCofactorBits=%d remBits=%v",
		st.Steps, st.MaxRemBits, st.MaxCofactorBits, st.RemBits)
	t.Logf("naive Euclid over Q: maxBits=%d", naiveBits)
	t.Logf("verification: mulOps=%d addOps=%d", st.VerifyMulOps, st.VerifyAddOps)

	// The fraction-free subresultant sequence must stay well below the
	// naive Euclidean coefficient growth.
	if st.MaxRemBits >= naiveBits {
		t.Fatalf("subresultant max bits %d not smaller than naive %d", st.MaxRemBits, naiveBits)
	}
	// Both compute the same gcd.
	g1 := GCD(A, B)
	if !g1.Equal(g2) {
		t.Fatalf("gcd mismatch: subresultant %v vs naive %v", g1, g2)
	}
	// The naive Bézout coefficients also satisfy the identity (sanity).
	if lhs := Add(Mul(u2, A), Mul(v2, B)); !lhs.Equal(g2) {
		t.Fatal("naive Bézout identity failed")
	}
}

func TestCertificateVerificationTamper(t *testing.T) {
	a := Mul(PolyFromInts(-1, 1), PolyFromInts(3, 0, 1)) // (x-1)(x^2+3)
	b := Mul(PolyFromInts(-1, 1), PolyFromInts(1, 1))    // (x-1)(x+1)
	g, u, v, _, err := ExtendedGCD(a, b)
	if err != nil {
		t.Fatal(err)
	}
	ok, _ := VerifyCertificate(a, b, g, u, v)
	if !ok {
		t.Fatal("valid certificate rejected")
	}
	// Tamper with u: the identity must break.
	bad := Add(u, PolyFromInts(1))
	if ok, _ := VerifyCertificate(a, b, g, bad, v); ok {
		t.Fatal("tampered Bézout coefficient accepted")
	}
	// Tamper with g: non-monic must be rejected.
	if ok, _ := VerifyCertificate(a, b, Scale(g, big.NewRat(2, 1)), u, v); ok {
		t.Fatal("non-monic g accepted")
	}
	// A non-divisor g must be rejected.
	if ok, _ := VerifyCertificate(a, b, PolyFromInts(-2, 1), u, v); ok {
		t.Fatal("non-divisor g accepted")
	}
	// Zero certificate conventions.
	if ok, _ := VerifyCertificate(Zero, Zero, Zero, Zero, Zero); !ok {
		t.Fatal("zero certificate rejected")
	}
	if ok, _ := VerifyCertificate(a, b, Zero, Zero, Zero); ok {
		t.Fatal("zero g accepted for nonzero inputs")
	}
}

func TestBitLengthStatsRecorded(t *testing.T) {
	a := Mul(pow(PolyFromInts(-1, 1), 2), PolyFromInts(5, 3, 0, 2))
	b := Mul(PolyFromInts(-1, 1), PolyFromInts(-4, 0, 1))
	_, _, _, st, err := ExtendedGCD(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if st.Steps == 0 || len(st.RemBits) == 0 {
		t.Fatalf("no stats recorded: %+v", st)
	}
	if st.MaxRemBits <= 0 || st.VerifyMulOps <= 0 || st.VerifyAddOps <= 0 {
		t.Fatalf("incomplete stats: %+v", st)
	}
	if !st.Verified {
		t.Fatal("certificate not verified")
	}
	t.Logf("stats: %+v remBits=%v", st, st.RemBits)
}
