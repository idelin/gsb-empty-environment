package trigcert

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

func mustContain(t *testing.T, name string, i Interval, v *big.Rat) {
	t.Helper()
	if !i.Contains(v) {
		t.Fatalf("%s: interval [%v, %v] does not contain %v", name, i.Lo, i.Hi, v)
	}
}

func mustOverlap(t *testing.T, name string, i, j Interval) {
	t.Helper()
	if !i.Overlaps(j) {
		t.Fatalf("%s: [%v, %v] disjoint from [%v, %v]", name, i.Lo, i.Hi, j.Lo, j.Hi)
	}
}

func mustBeWithin(t *testing.T, name string, i Interval, lo, hi *big.Rat) {
	t.Helper()
	if i.Lo.Cmp(lo) < 0 || i.Hi.Cmp(hi) > 0 {
		t.Fatalf("%s: [%v, %v] not within [%v, %v]", name, i.Lo, i.Hi, lo, hi)
	}
}

func widthLE(t *testing.T, name string, i Interval, bound *big.Rat) {
	t.Helper()
	if i.Width().Cmp(bound) > 0 {
		t.Fatalf("%s: width %v exceeds %v (interval [%v, %v])", name, i.Width(), bound, i.Lo, i.Hi)
	}
}

// pythag returns the interval sinI^2 + cosI^2 (auxiliary identity check).
func pythag(sinI, cosI Interval) Interval {
	return sinI.Mul(sinI).Add(cosI.Mul(cosI))
}

// refSinCos is an independent reference implementation used only in tests:
// a direct Taylor summation (no range reduction, no budget) valid for
// |x| <= 3, returning a certified enclosure via the alternating-series
// remainder bound.
func refSinCos(t *testing.T, x *big.Rat, terms int) (Interval, Interval) {
	t.Helper()
	if abs := new(big.Rat).Abs(x); abs.Cmp(big.NewRat(3, 1)) > 0 {
		t.Fatalf("refSinCos only valid for |x| <= 3")
	}
	x2 := new(big.Rat).Mul(x, x)

	sumSin := new(big.Rat)
	termSin := new(big.Rat).Set(x)
	sumCos := new(big.Rat)
	termCos := new(big.Rat).SetInt64(1)
	for i := 0; i < terms; i++ {
		sumSin.Add(sumSin, termSin)
		sumCos.Add(sumCos, termCos)
		// Next terms via direct recurrence (independent code path).
		d1 := int64(2*i+2) * int64(2*i+3)
		termSin.Mul(termSin, x2)
		termSin.Neg(termSin)
		termSin.Quo(termSin, new(big.Rat).SetInt64(d1))
		d2 := int64(2*i+1) * int64(2*i+2)
		termCos.Mul(termCos, x2)
		termCos.Neg(termCos)
		termCos.Quo(termCos, new(big.Rat).SetInt64(d2))
	}
	remSin := new(big.Rat).Abs(termSin)
	remCos := new(big.Rat).Abs(termCos)
	sinI := NewInterval(new(big.Rat).Sub(sumSin, remSin), new(big.Rat).Add(sumSin, remSin))
	cosI := NewInterval(new(big.Rat).Sub(sumCos, remCos), new(big.Rat).Add(sumCos, remCos))
	return sinI, cosI
}

// --- Certified pi -------------------------------------------------------

func TestPiIntervalCertified(t *testing.T) {
	pi := PiInterval(30)
	// The interval must overlap the window
	// [3.1415926535897932, 3.1415926535897933], which contains pi.
	win := NewInterval(rat(t, "31415926535897932/10000000000000000"),
		rat(t, "31415926535897933/10000000000000000"))
	mustOverlap(t, "pi window", pi, win)
	// And it must exclude values known to differ from pi.
	if pi.Contains(rat(t, "3")) || pi.Contains(rat(t, "4")) || pi.Contains(rat(t, "22/7")) {
		t.Fatalf("pi interval [%v, %v] contains a known non-pi value", pi.Lo, pi.Hi)
	}
	widthLE(t, "pi", pi, rat(t, "1/10000000000000000000")) // 1e-19
	// Nesting: a coarser computation must contain a finer one's midpoint.
	mustContain(t, "pi nesting", PiInterval(4), PiInterval(30).Mid())
}

// --- Zeros --------------------------------------------------------------

func TestZeroAngle(t *testing.T) {
	sinI, cosI := SinCos(big.NewRat(0, 1), rat(t, "1/1000000"), 100)
	if sinI.Lo.Sign() != 0 || sinI.Hi.Sign() != 0 {
		t.Fatalf("sin(0) not exactly [0,0]: [%v, %v]", sinI.Lo, sinI.Hi)
	}
	if cosI.Lo.Cmp(ratOne) != 0 || cosI.Hi.Cmp(ratOne) != 0 {
		t.Fatalf("cos(0) not exactly [1,1]: [%v, %v]", cosI.Lo, cosI.Hi)
	}
}

// --- Near pi/2 ----------------------------------------------------------

func TestNearPiOver2(t *testing.T) {
	// x is the exact rational midpoint of a very tight certified pi
	// interval, halved: x = pi/2 up to ~1e-56.
	x := new(big.Rat).Quo(PiInterval(40).Mid(), big.NewRat(2, 1))
	eps := rat(t, "1/1000000000000") // 1e-12
	sinI, cosI := SinCos(x, eps, 2000)

	// sin(x) is within (pi/2 - x)^2/2 < 1e-100 of 1.
	gap := new(big.Rat).Sub(ratOne, sinI.Lo)
	if gap.Cmp(rat(t, "1/1000000000")) > 0 {
		t.Fatalf("sin near pi/2 too far below 1: gap %v", gap)
	}
	if sinI.Hi.Cmp(ratOne) > 0 {
		t.Fatalf("sin exceeds 1: hi = %v", sinI.Hi)
	}
	// cos(x) is within ~1e-56 of 0, so its enclosure must hug 0.
	mustContain(t, "cos near pi/2", cosI, ratZero)
	widthLE(t, "cos near pi/2", cosI, rat(t, "1/1000000000"))

	// Auxiliary: Pythagorean identity must hold as interval containment.
	mustContain(t, "sin^2+cos^2", pythag(sinI, cosI), ratOne)
}

// --- Known values at small angles ---------------------------------------

func TestKnownSmallValues(t *testing.T) {
	eps := rat(t, "1/1000000000000") // 1e-12
	// Independent rational windows bracketing the true values:
	//   sin(1)  = 0.84147098480789650665...
	//   cos(1)  = 0.54030230586813971740...
	//   sin(-2) = -0.90929742682568169539...
	//   cos(-2) = -0.41614683654714238699...
	cases := []struct {
		x            string
		sinLo, sinHi string
		cosLo, cosHi string
	}{
		{"1", "841470984807896/1000000000000000", "841470984807897/1000000000000000",
			"540302305868139/1000000000000000", "540302305868140/1000000000000000"},
		{"-2", "-909297426825682/1000000000000000", "-909297426825681/1000000000000000",
			"-416146836547143/1000000000000000", "-416146836547142/1000000000000000"},
	}
	for _, c := range cases {
		sinI, cosI := SinCos(rat(t, c.x), eps, 2000)
		widthLE(t, "sin("+c.x+")", sinI, eps)
		widthLE(t, "cos("+c.x+")", cosI, eps)
		sinWin := NewInterval(rat(t, c.sinLo), rat(t, c.sinHi))
		cosWin := NewInterval(rat(t, c.cosLo), rat(t, c.cosHi))
		// Both intervals contain the true value, so they must overlap,
		// and the enclosure must stay within the window expanded by eps.
		mustOverlap(t, "sin("+c.x+")", sinI, sinWin)
		mustOverlap(t, "cos("+c.x+")", cosI, cosWin)
		mustBeWithin(t, "sin("+c.x+")", sinI, sinWin.Expand(eps).Lo, sinWin.Expand(eps).Hi)
		mustBeWithin(t, "cos("+c.x+")", cosI, cosWin.Expand(eps).Lo, cosWin.Expand(eps).Hi)
	}
}

// --- Cross-check against an independent reference series -----------------

func TestReferenceCrossCheck(t *testing.T) {
	eps := rat(t, "1/1000000000000")
	for _, xs := range []string{"1", "-2", "3/7", "5/2", "-1/11"} {
		x := rat(t, xs)
		sinI, cosI := SinCos(x, eps, 2000)
		refSin, refCos := refSinCos(t, x, 40)
		mustOverlap(t, "sin("+xs+") vs ref", sinI, refSin)
		mustOverlap(t, "cos("+xs+") vs ref", cosI, refCos)
		widthLE(t, "sin("+xs+")", sinI, eps)
		widthLE(t, "cos("+xs+")", cosI, eps)
	}
}

// --- Huge angles --------------------------------------------------------

func TestHugeAngles(t *testing.T) {
	eps := rat(t, "1/1000000000") // 1e-9
	for _, xs := range []string{"30000001/3", "-30000001/3", "123456789012345/7"} {
		x := rat(t, xs)
		sinI, cosI := SinCos(x, eps, 20000)
		widthLE(t, "sin("+xs+")", sinI, eps)
		widthLE(t, "cos("+xs+")", cosI, eps)

		// Auxiliary identity checks (never the source of the guarantee).
		mustContain(t, "sin^2+cos^2 ("+xs+")", pythag(sinI, cosI), ratOne)

		// Double-angle identity computed independently at x/2.
		half := new(big.Rat).Quo(x, big.NewRat(2, 1))
		s2, c2 := SinCos(half, eps, 20000)
		dbl := s2.Mul(c2).Scale(big.NewRat(2, 1))
		mustOverlap(t, "sin(2u)=2sin(u)cos(u) ("+xs+")", sinI, dbl)
	}
}

// --- Budget exhaustion ---------------------------------------------------

func TestBudgetExhaustion(t *testing.T) {
	// Zero budget: the trivially valid enclosure.
	sinI, cosI := SinCos(rat(t, "1/2"), rat(t, "1/1000000"), 0)
	for _, i := range []Interval{sinI, cosI} {
		if i.Lo.Cmp(big.NewRat(-1, 1)) != 0 || i.Hi.Cmp(ratOne) != 0 {
			t.Fatalf("budget 0 should give [-1,1], got [%v, %v]", i.Lo, i.Hi)
		}
	}

	// Tiny budgets: still valid enclosures of sin(1/2), cos(1/2), just
	// possibly wider than eps. Cross-checked against the reference series.
	refSin, refCos := refSinCos(t, rat(t, "1/2"), 40)
	for _, budget := range []int{1, 2, 3, 5, 8, 13} {
		s, c := SinCos(rat(t, "1/2"), rat(t, "1/1000000000000"), budget)
		mustOverlap(t, "sin tiny budget", s, refSin)
		mustOverlap(t, "cos tiny budget", c, refCos)
	}
}

// --- Auxiliary identity sweep --------------------------------------------

func TestIdentitiesSweep(t *testing.T) {
	eps := rat(t, "1/1000000000")
	angles := []string{"1/11", "-3/7", "22/7", "100", "-271828/1000", "5/2"}
	for _, xs := range angles {
		x := rat(t, xs)
		sinI, cosI := SinCos(x, eps, 5000)
		mustContain(t, "sin^2+cos^2 ("+xs+")", pythag(sinI, cosI), ratOne)

		// sin(x + 3/11) overlaps sin(x)cos(3/11) + cos(x)sin(3/11).
		b := rat(t, "3/11")
		sb, cb := SinCos(b, eps, 5000)
		sum := sinI.Mul(cb).Add(cosI.Mul(sb))
		xb := new(big.Rat).Add(x, b)
		sSum, _ := SinCos(xb, eps, 5000)
		mustOverlap(t, "sin("+xs+" + 3/11)", sSum, sum)
	}
}

// --- Budget too small for a huge angle ------------------------------------

func TestHugeAngleTinyBudget(t *testing.T) {
	// eps is unreachable with this budget; the result must still be a
	// valid enclosure, which the Pythagorean identity verifies
	// (auxiliary check): valid enclosures always satisfy 1 in s^2+c^2.
	x := rat(t, "30000001/3")
	eps := rat(t, "1/1000000000000000000000000000000") // 1e-30
	sinI, cosI := SinCos(x, eps, 30)
	mustContain(t, "sin^2+cos^2 tiny budget", pythag(sinI, cosI), ratOne)
	// Enclosures always lie within [-1, 1].
	mustBeWithin(t, "sin tiny budget", sinI, big.NewRat(-1, 1), ratOne)
	mustBeWithin(t, "cos tiny budget", cosI, big.NewRat(-1, 1), ratOne)
}
