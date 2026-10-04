package certitrig

import (
	"math"
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

func pow10(n int64) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(n), nil)
}

// epsRat returns 10^-n.
func epsRat(n int64) *big.Rat {
	return new(big.Rat).SetFrac(big.NewInt(1), pow10(n))
}

// distToInterval returns 0 if x is in [lo,hi], else the distance to it.
func distToInterval(iv Interval, x *big.Rat) *big.Rat {
	if iv.Contains(x) {
		return new(big.Rat)
	}
	if x.Cmp(iv.Lo) < 0 {
		return new(big.Rat).Sub(iv.Lo, x)
	}
	return new(big.Rat).Sub(x, iv.Hi)
}

// assertNearRef checks the auxiliary containment property against a
// float64 reference, tolerating the reference's own rounding error.
func assertNearRef(t *testing.T, iv Interval, ref float64, tol *big.Rat) {
	t.Helper()
	d := distToInterval(iv, floatRat(ref))
	if d.Cmp(tol) > 0 {
		t.Fatalf("enclosure %v is %v away from reference %v (tol %v)",
			iv, d.FloatString(30), ref, tol.FloatString(30))
	}
}

func floatRat(f float64) *big.Rat {
	r, ok := new(big.Rat).SetString(new(big.Float).SetFloat64(f).Text('g', 17))
	if !ok {
		// Fallback: exact binary rational of the float64.
		r = new(big.Rat).SetFloat64(f)
	}
	return r
}

func TestPiIntervalCertified(t *testing.T) {
	delta := epsRat(40)
	pi := PiInterval(delta, &Budget{Left: 10000})
	if pi.Width().Cmp(delta) > 0 {
		t.Fatalf("pi interval width exceeds delta: %v", pi.Width().FloatString(50))
	}
}

func TestPiIntervalDigits(t *testing.T) {
	pi := PiInterval(epsRat(40), &Budget{Left: 10000})
	lo := rat(t, "3.14159265358979323846264338327950288419716939937509")
	hi := rat(t, "3.14159265358979323846264338327950288419716939937512")
	if pi.Lo.Cmp(lo) > 0 || pi.Hi.Cmp(hi) < 0 {
		t.Fatalf("pi interval %v inconsistent with known digits", pi)
	}
}

func TestZero(t *testing.T) {
	eps := epsRat(30)
	s, c := SinCos(big.NewRat(0, 1), eps, &Budget{Left: 10000})
	if !s.Contains(big.NewRat(0, 1)) {
		t.Fatalf("sin(0) interval %v does not contain 0", s)
	}
	if !c.Contains(big.NewRat(1, 1)) {
		t.Fatalf("cos(0) interval %v does not contain 1", c)
	}
	if s.Width().Cmp(eps) > 0 || c.Width().Cmp(eps) > 0 {
		t.Fatalf("widths exceed eps: sin %v cos %v", s, c)
	}
}

func TestNearPiOver2(t *testing.T) {
	// x approximates pi/2 to 30 digits (slightly above the true value).
	x := rat(t, "15707963267948966192313216916398/10000000000000000000000000000000")
	eps := epsRat(20)
	s, c := SinCos(x, eps, &Budget{Left: 100000})
	if s.Width().Cmp(eps) > 0 || c.Width().Cmp(eps) > 0 {
		t.Fatalf("widths exceed eps: sin %v cos %v", s, c)
	}
	one := big.NewRat(1, 1)
	// sin(x) is within 1e-60 of 1, so the enclosure must reach up to ~1.
	if s.Hi.Cmp(one) > 0 {
		t.Fatalf("sin enclosure exceeds 1: %v", s)
	}
	if new(big.Rat).Sub(one, s.Hi).Cmp(epsRat(30)) > 0 {
		t.Fatalf("sin enclosure top too far below 1: %v", s)
	}
	// cos(x) ~ -5e-32: enclosure must contain a near-zero point.
	if c.Lo.Cmp(epsRat(25)) > 0 || c.Hi.Cmp(new(big.Rat).Neg(epsRat(25))) < 0 {
		t.Fatalf("cos enclosure not near zero: %v", c)
	}
	assertIdentity(t, s, c)
}

func TestHugeAngle(t *testing.T) {
	cases := []*big.Rat{
		new(big.Rat).SetInt(pow10(18)),
		new(big.Rat).SetInt(new(big.Int).Add(pow10(30), big.NewInt(7))),
		new(big.Rat).SetInt(new(big.Int).Neg(pow10(18))),
		rat(t, "123456789012345678901234567890/7"),
	}
	eps := epsRat(12)
	for _, x := range cases {
		s, c := SinCos(x, eps, &Budget{Left: 1000000})
		if s.Width().Cmp(eps) > 0 || c.Width().Cmp(eps) > 0 {
			t.Fatalf("x=%v: widths exceed eps: sin %v cos %v", x, s, c)
		}
		assertIdentity(t, s, c)
	}
	// Auxiliary cross-check against Go's Payne-Hanek reduced math.Sin
	// for the exactly float64-representable 1e18.
	x := new(big.Rat).SetInt(pow10(18))
	s, c := SinCos(x, eps, &Budget{Left: 1000000})
	xf, _ := x.Float64()
	tol := epsRat(9)
	assertNearRef(t, s, math.Sin(xf), tol)
	assertNearRef(t, c, math.Cos(xf), tol)
}

func TestQuadrants(t *testing.T) {
	eps := epsRat(25)
	for _, xs := range []string{"1/2", "2", "7/2", "5", "-1/2", "-2", "-7/2", "-5", "355/113", "-355/113"} {
		x := rat(t, xs)
		s, c := SinCos(x, eps, &Budget{Left: 100000})
		if s.Width().Cmp(eps) > 0 || c.Width().Cmp(eps) > 0 {
			t.Fatalf("x=%s: widths exceed eps: sin %v cos %v", xs, s, c)
		}
		xf, _ := x.Float64()
		tol := epsRat(14)
		assertNearRef(t, s, math.Sin(xf), tol)
		assertNearRef(t, c, math.Cos(xf), tol)
		assertIdentity(t, s, c)
	}
}

func TestBudgetExhaustion(t *testing.T) {
	x := rat(t, "1/2")
	eps := epsRat(20)

	// Zero budget: the trivially valid [-1,1] enclosures.
	s, c := SinCos(x, eps, &Budget{Left: 0})
	one := big.NewRat(1, 1)
	if s.Lo.Cmp(new(big.Rat).Neg(one)) != 0 || s.Hi.Cmp(one) != 0 {
		t.Fatalf("zero-budget sin not [-1,1]: %v", s)
	}
	if c.Lo.Cmp(new(big.Rat).Neg(one)) != 0 || c.Hi.Cmp(one) != 0 {
		t.Fatalf("zero-budget cos not [-1,1]: %v", c)
	}

	// Every budget level must still return a *legal* enclosure.
	xf, _ := x.Float64()
	sref, cref := math.Sin(xf), math.Cos(xf)
	tol := epsRat(14)
	prevSW, prevCW := new(big.Rat), new(big.Rat)
	for _, b := range []int64{0, 1, 2, 5, 10, 25, 60, 200} {
		s, c := SinCos(x, eps, &Budget{Left: b})
		assertNearRef(t, s, sref, tol)
		assertNearRef(t, c, cref, tol)
		if b > 0 {
			if s.Width().Cmp(prevSW) > 0 || c.Width().Cmp(prevCW) > 0 {
				t.Fatalf("budget %d: enclosure wider than smaller budget", b)
			}
		}
		prevSW, prevCW = s.Width(), c.Width()
	}
}

func TestIdentityAuxiliary(t *testing.T) {
	eps := epsRat(15)
	xs := []*big.Rat{
		big.NewRat(0, 1),
		rat(t, "22/7"),
		rat(t, "-104348/33215"),
		new(big.Rat).SetInt(pow10(24)),
		rat(t, "15707963267948966192313216916398/10000000000000000000000000000000"),
	}
	for _, x := range xs {
		s, c := SinCos(x, eps, &Budget{Left: 1000000})
		assertIdentity(t, s, c)
	}
}

// assertIdentity checks the auxiliary (non-authoritative) identity
// sin^2 + cos^2 == 1 via interval arithmetic.
func assertIdentity(t *testing.T, s, c Interval) {
	t.Helper()
	id := s.Square().Add(c.Square())
	if !id.Contains(big.NewRat(1, 1)) {
		t.Fatalf("sin^2+cos^2 enclosure %v does not contain 1 (sin %v cos %v)", id, s, c)
	}
}
