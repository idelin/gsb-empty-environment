package ratsimp

import (
	"math/big"
	"testing"
)

func mustRational(t *testing.T, num, den *Poly) *Rational {
	t.Helper()
	r, err := NewRational(num, den)
	if err != nil {
		t.Fatalf("NewRational: %v", err)
	}
	return r
}

func excludedAt(t *testing.T, s *Simplified, x int64) ExcludedPoint {
	t.Helper()
	for _, e := range s.Excluded {
		if e.Where.Contains(big.NewRat(x, 1)) {
			return e
		}
	}
	t.Fatalf("no excluded point at x=%d (got %v)", x, s.Excluded)
	return ExcludedPoint{}
}

// (x^2-1)/(x-1) reduces to x+1 but keeps a hole at x=1.
func TestHolePreserved(t *testing.T) {
	r := mustRational(t, PolyFromInts(-1, 0, 1), PolyFromInts(-1, 1))
	s := Simplify(r)

	if !s.Num.Equal(PolyFromInts(1, 1)) || !s.Den.Equal(PolyFromInts(1)) {
		t.Fatalf("reduced = (%s)/(%s), want (x + 1)/(1)", s.Num, s.Den)
	}
	if len(s.Excluded) != 1 {
		t.Fatalf("got %d excluded points, want 1", len(s.Excluded))
	}
	e := s.Excluded[0]
	if e.Kind != Hole {
		t.Errorf("kind = %v, want Hole", e.Kind)
	}
	if e.OriginalMultiplicity != 1 || e.PoleOrder != 0 {
		t.Errorf("multiplicity=%d poleOrder=%d, want 1 and 0",
			e.OriginalMultiplicity, e.PoleOrder)
	}
	if !e.Where.Contains(big.NewRat(1, 1)) {
		t.Errorf("interval %v does not contain 1", e.Where)
	}
}

// (x-1)^2 (x+2) / ((x-1)^3 (x-3)): x=1 stays a pole of order 1 after
// partial cancellation; x=3 is a pole of order 1.
func TestRepeatedFactors(t *testing.T) {
	xm1 := PolyFromInts(-1, 1)
	num := xm1.Pow(2).Mul(PolyFromInts(2, 1))
	den := xm1.Pow(3).Mul(PolyFromInts(-3, 1))
	s := Simplify(mustRational(t, num, den))

	if !s.Num.Equal(PolyFromInts(2, 1)) || !s.Den.Equal(PolyFromInts(3, -4, 1)) {
		t.Fatalf("reduced = (%s)/(%s), want (x + 2)/(x^2 - 4x + 3)", s.Num, s.Den)
	}
	if len(s.Excluded) != 2 {
		t.Fatalf("got %d excluded points, want 2", len(s.Excluded))
	}
	e1 := excludedAt(t, s, 1)
	if e1.Kind != Pole || e1.PoleOrder != 1 || e1.OriginalMultiplicity != 3 {
		t.Errorf("at x=1: kind=%v poleOrder=%d mult=%d, want Pole/1/3",
			e1.Kind, e1.PoleOrder, e1.OriginalMultiplicity)
	}
	e3 := excludedAt(t, s, 3)
	if e3.Kind != Pole || e3.PoleOrder != 1 || e3.OriginalMultiplicity != 1 {
		t.Errorf("at x=3: kind=%v poleOrder=%d mult=%d, want Pole/1/1",
			e3.Kind, e3.PoleOrder, e3.OriginalMultiplicity)
	}
}

// (x-1)^2/(x-1)^2 reduces to 1 but x=1 remains a hole of
// multiplicity 2.
func TestFullyCanceledRepeatedFactor(t *testing.T) {
	xm1 := PolyFromInts(-1, 1)
	r := mustRational(t, xm1.Pow(2), xm1.Pow(2))
	s := Simplify(r)

	if !s.Num.Equal(PolyFromInts(1)) || !s.Den.Equal(PolyFromInts(1)) {
		t.Fatalf("reduced = (%s)/(%s), want 1/1", s.Num, s.Den)
	}
	e := excludedAt(t, s, 1)
	if e.Kind != Hole || e.OriginalMultiplicity != 2 || e.PoleOrder != 0 {
		t.Errorf("kind=%v mult=%d poleOrder=%d, want Hole/2/0",
			e.Kind, e.OriginalMultiplicity, e.PoleOrder)
	}
}

// 0/(x^2-4) is the zero function with holes at x=-2 and x=2.
func TestZeroNumerator(t *testing.T) {
	r := mustRational(t, PolyFromInts(), PolyFromInts(-4, 0, 1))
	s := Simplify(r)

	if !s.Num.IsZero() || !s.Den.Equal(PolyFromInts(1)) {
		t.Fatalf("reduced = (%s)/(%s), want 0/1", s.Num, s.Den)
	}
	if len(s.Excluded) != 2 {
		t.Fatalf("got %d excluded points, want 2", len(s.Excluded))
	}
	for _, x := range []int64{-2, 2} {
		e := excludedAt(t, s, x)
		if e.Kind != Hole || e.OriginalMultiplicity != 1 || e.PoleOrder != 0 {
			t.Errorf("at x=%d: kind=%v mult=%d poleOrder=%d, want Hole/1/0",
				x, e.Kind, e.OriginalMultiplicity, e.PoleOrder)
		}
	}
}

func TestEquivalenceModes(t *testing.T) {
	// f1 = (x^2-1)/(x-1), f2 = x+1: equal wherever both are defined,
	// but f1 has a hole at x=1.
	f1 := mustRational(t, PolyFromInts(-1, 0, 1), PolyFromInts(-1, 1))
	f2 := mustRational(t, PolyFromInts(1, 1), PolyFromInts(1))
	if !Equivalent(f1, f2, CommonDomain) {
		t.Error("f1 and f2 should be equivalent on the common domain")
	}
	if Equivalent(f1, f2, SameDomain) {
		t.Error("f1 and f2 should not be equivalent on the same domain")
	}

	// f3 = (x+1)(x-1)^2/(x-1)^2: same domain as f1, equal on it.
	xm1 := PolyFromInts(-1, 1)
	f3 := mustRational(t, PolyFromInts(1, 1).Mul(xm1.Pow(2)), xm1.Pow(2))
	if !Equivalent(f1, f3, SameDomain) {
		t.Error("f1 and f3 should be equivalent on the same domain")
	}

	// Different values on the common domain: not equivalent at all.
	f4 := mustRational(t, PolyFromInts(2, 1), PolyFromInts(1))
	if Equivalent(f1, f4, CommonDomain) {
		t.Error("f1 and f4 differ on the common domain")
	}
}

func TestZeroNumeratorEquivalence(t *testing.T) {
	z1 := mustRational(t, PolyFromInts(), PolyFromInts(-1, 1))    // 0/(x-1)
	z2 := mustRational(t, PolyFromInts(), PolyFromInts(-2, 1))    // 0/(x-2)
	z3 := mustRational(t, PolyFromInts(), PolyFromInts(-2, 2))    // 0/(2x-2)
	z4 := mustRational(t, PolyFromInts(), PolyFromInts(-1, 0, 1)) // 0/(x^2-1)

	if !Equivalent(z1, z2, CommonDomain) {
		t.Error("0/(x-1) and 0/(x-2) agree on the common domain")
	}
	if Equivalent(z1, z2, SameDomain) {
		t.Error("0/(x-1) and 0/(x-2) have different domains")
	}
	if !Equivalent(z1, z3, SameDomain) {
		t.Error("0/(x-1) and 0/(2x-2) have the same domain")
	}
	if Equivalent(z1, z4, SameDomain) {
		t.Error("0/(x-1) and 0/(x^2-1) have different domains")
	}
}

// 1/(x^2-2): the poles at +/-sqrt(2) must be isolated exactly, with
// rational endpoints, and refinable to any width.
func TestIrrationalRootIsolation(t *testing.T) {
	r := mustRational(t, PolyFromInts(1), PolyFromInts(-2, 0, 1))
	s := Simplify(r)

	if len(s.Excluded) != 2 {
		t.Fatalf("got %d excluded points, want 2", len(s.Excluded))
	}
	two := big.NewRat(2, 1)
	for i, e := range s.Excluded {
		if e.Kind != Pole || e.PoleOrder != 1 {
			t.Errorf("point %d: kind=%v poleOrder=%d, want Pole/1", i, e.Kind, e.PoleOrder)
		}
		// The interval must isolate a root of x^2-2: the square of
		// the endpoints brackets 2.
		lo2 := new(big.Rat).Mul(e.Where.Lo, e.Where.Lo)
		hi2 := new(big.Rat).Mul(e.Where.Hi, e.Where.Hi)
		if !(lo2.Cmp(two) < 0 && hi2.Cmp(two) >= 0) &&
			!(hi2.Cmp(two) < 0 && lo2.Cmp(two) >= 0) {
			t.Errorf("point %d: interval %v does not bracket sqrt(2)", i, e.Where)
		}
		refined := e.Refine(big.NewRat(1, 1000))
		if refined.Width().Cmp(big.NewRat(1, 1000)) > 0 {
			t.Errorf("point %d: refined width %s > 1/1000", i, refined.Width())
		}
		mid := new(big.Rat).Add(refined.Lo, refined.Hi)
		mid.Quo(mid, big.NewRat(2, 1))
		f, _ := mid.Float64()
		abs := f
		if abs < 0 {
			abs = -abs
		}
		if d := abs - 1.4142135623730951; d < -1e-3 || d > 1e-3 {
			t.Errorf("point %d: refined midpoint %v not near sqrt(2)", i, f)
		}
	}
	// The two intervals must be disjoint and ordered.
	if s.Excluded[0].Where.Hi.Cmp(s.Excluded[1].Where.Lo) >= 0 {
		t.Error("isolating intervals are not disjoint and ordered")
	}
}

func TestZeroDenominatorRejected(t *testing.T) {
	if _, err := NewRational(PolyFromInts(1), PolyFromInts()); err == nil {
		t.Error("expected error for zero denominator")
	}
}

// 1/(x^2+1) is defined everywhere: no excluded points.
func TestNoRealRoots(t *testing.T) {
	r := mustRational(t, PolyFromInts(1), PolyFromInts(1, 0, 1))
	s := Simplify(r)
	if len(s.Excluded) != 0 {
		t.Fatalf("got %d excluded points, want 0", len(s.Excluded))
	}
}

// (x^2+1)/((x-1)(x^2+1)) and 1/(x-1) exclude exactly the same real
// points and agree on them, even though the denominators differ as
// polynomials (x^2+1 has no real roots).
func TestSameDomainWithComplexFactors(t *testing.T) {
	f1 := mustRational(t, PolyFromInts(1, 0, 1),
		PolyFromInts(-1, 1).Mul(PolyFromInts(1, 0, 1)))
	f2 := mustRational(t, PolyFromInts(1), PolyFromInts(-1, 1))
	if !Equivalent(f1, f2, SameDomain) {
		t.Error("domains should be equal: only x=1 is excluded in both")
	}
}

// (2x+2)/(4x+2) should be normalized to (x+1)/(2x+1).
func TestContentNormalization(t *testing.T) {
	r := mustRational(t, PolyFromInts(2, 2), PolyFromInts(2, 4))
	s := Simplify(r)
	if !s.Num.Equal(PolyFromInts(1, 1)) || !s.Den.Equal(PolyFromInts(1, 2)) {
		t.Fatalf("reduced = (%s)/(%s), want (x + 1)/(2x + 1)", s.Num, s.Den)
	}
	if len(s.Excluded) != 1 {
		t.Fatalf("got %d excluded points, want 1", len(s.Excluded))
	}
	if !s.Excluded[0].Where.Contains(big.NewRat(-1, 2)) {
		t.Errorf("interval %v does not contain -1/2", s.Excluded[0].Where)
	}
}
