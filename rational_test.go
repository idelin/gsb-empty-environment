package rational

import (
	"math/big"
	"testing"
)

func mustSimplify(t *testing.T, f RationalFunction) *Simplified {
	t.Helper()
	s, err := Simplify(f)
	if err != nil {
		t.Fatalf("Simplify(%v / %v): %v", f.Num, f.Den, err)
	}
	return s
}

func rootNear(t *testing.T, roots []IsolatedRoot, x int64) IsolatedRoot {
	t.Helper()
	target := big.NewRat(x, 1)
	for i := range roots {
		if roots[i].Contains(target) {
			return roots[i]
		}
	}
	t.Fatalf("no isolated root contains %d among %v", x, roots)
	return IsolatedRoot{}
}

func TestHoleIsPreserved(t *testing.T) {
	// f = (x-1)(x-2) / ((x-1)(x-3)): hole at 1, pole at 3.
	f := RationalFunction{
		Num: PolyFromInts(2, -3, 1),
		Den: PolyFromInts(3, -4, 1),
	}
	s := mustSimplify(t, f)
	if !s.Num.Equal(PolyFromInts(-2, 1)) {
		t.Errorf("reduced numerator = %v, want x-2", s.Num)
	}
	if !s.Den.Equal(PolyFromInts(-3, 1)) {
		t.Errorf("reduced denominator = %v, want x-3", s.Den)
	}
	if len(s.Holes) != 1 {
		t.Fatalf("holes = %v, want exactly one (x=1)", s.Holes)
	}
	rootNear(t, s.Holes, 1)
	if len(s.Poles) != 1 || s.Poles[0].Multiplicity != 1 {
		t.Fatalf("poles = %+v, want one pole of multiplicity 1", s.Poles)
	}
	rootNear(t, []IsolatedRoot{s.Poles[0].Root}, 3)
	if len(s.Excluded) != 2 {
		t.Fatalf("excluded = %v, want {1, 3}", s.Excluded)
	}
	rootNear(t, s.Excluded, 1)
	rootNear(t, s.Excluded, 3)
}

func TestRepeatedFactors(t *testing.T) {
	// num = (x-1)^2 (x-3), den = (x-1)^2 (x-2)^2 (x-3):
	// holes at 1 and 3, pole at 2 with multiplicity 2.
	xm1 := PolyFromInts(-1, 1)
	xm2 := PolyFromInts(-2, 1)
	xm3 := PolyFromInts(-3, 1)
	f := RationalFunction{
		Num: Mul(Mul(xm1, xm1), xm3),
		Den: Mul(Mul(Mul(xm1, xm1), Mul(xm2, xm2)), xm3),
	}
	s := mustSimplify(t, f)
	if !s.Num.Equal(PolyFromInts(1)) {
		t.Errorf("reduced numerator = %v, want 1", s.Num)
	}
	if !s.Den.Equal(PolyFromInts(4, -4, 1)) {
		t.Errorf("reduced denominator = %v, want (x-2)^2", s.Den)
	}
	if len(s.Holes) != 2 {
		t.Fatalf("holes = %v, want {1, 3}", s.Holes)
	}
	rootNear(t, s.Holes, 1)
	rootNear(t, s.Holes, 3)
	if len(s.Poles) != 1 || s.Poles[0].Multiplicity != 2 {
		t.Fatalf("poles = %+v, want one pole of multiplicity 2", s.Poles)
	}
	rootNear(t, []IsolatedRoot{s.Poles[0].Root}, 2)
	if len(s.Excluded) != 3 {
		t.Fatalf("excluded = %v, want {1, 2, 3}", s.Excluded)
	}
}

func TestPartialCancellationKeepsPole(t *testing.T) {
	// num = (x-1)^2, den = (x-1)^3: no hole at 1, pole of order 1.
	xm1 := PolyFromInts(-1, 1)
	f := RationalFunction{
		Num: Mul(xm1, xm1),
		Den: Mul(Mul(xm1, xm1), xm1),
	}
	s := mustSimplify(t, f)
	if !s.Den.Equal(PolyFromInts(-1, 1)) {
		t.Errorf("reduced denominator = %v, want x-1", s.Den)
	}
	if len(s.Holes) != 0 {
		t.Errorf("holes = %v, want none", s.Holes)
	}
	if len(s.Poles) != 1 || s.Poles[0].Multiplicity != 1 {
		t.Fatalf("poles = %+v, want one pole of multiplicity 1", s.Poles)
	}
}

func TestZeroNumerator(t *testing.T) {
	// 0 / (x^2-1): the zero function on R \ {-1, 1}; both points are holes.
	s := mustSimplify(t, RationalFunction{Num: Zero(), Den: PolyFromInts(-1, 0, 1)})
	if !s.Num.IsZero() {
		t.Errorf("reduced numerator = %v, want 0", s.Num)
	}
	if !s.Den.IsOne() {
		t.Errorf("reduced denominator = %v, want 1", s.Den)
	}
	if len(s.Poles) != 0 {
		t.Errorf("poles = %+v, want none", s.Poles)
	}
	if len(s.Holes) != 2 || len(s.Excluded) != 2 {
		t.Fatalf("holes = %v, excluded = %v, want {-1, 1}", s.Holes, s.Excluded)
	}
	rootNear(t, s.Holes, -1)
	rootNear(t, s.Holes, 1)
}

func TestRationalRootHitExactlyByBisection(t *testing.T) {
	// Hole at x = 1/2, which bisection can land on exactly.
	num := PolyFromInts(-1, 2)           // 2x-1
	den := Mul(num, PolyFromInts(-5, 1)) // (2x-1)(x-5)
	s := mustSimplify(t, RationalFunction{Num: num, Den: den})
	if !s.Num.IsOne() || !s.Den.Equal(PolyFromInts(-5, 1)) {
		t.Errorf("reduced = %v / %v, want 1 / (x-5)", s.Num, s.Den)
	}
	if len(s.Holes) != 1 {
		t.Fatalf("holes = %v, want one at 1/2", s.Holes)
	}
	half := big.NewRat(1, 2)
	if !s.Holes[0].Contains(half) {
		t.Errorf("hole interval %v does not contain 1/2", &s.Holes[0])
	}
}

func TestIrrationalRootsExactlyIsolated(t *testing.T) {
	// (x^2-2) / ((x^2-2)(x-1)): holes at +/-sqrt(2), pole at 1.
	num := PolyFromInts(-2, 0, 1)
	den := Mul(num, PolyFromInts(-1, 1))
	s := mustSimplify(t, RationalFunction{Num: num, Den: den})
	if len(s.Holes) != 2 {
		t.Fatalf("holes = %v, want two (at +/-sqrt(2))", s.Holes)
	}
	pos := s.Holes[1]
	if pos.Lo.Sign() <= 0 {
		t.Fatalf("expected positive hole second, got %v", s.Holes)
	}
	// Refine to exact rational width below 1/10000 and check sqrt(2).
	pos.RefineTo(big.NewRat(1, 10000))
	if pos.Width().Cmp(big.NewRat(1, 10000)) > 0 {
		t.Errorf("refined width %v still > 1/10000", pos.Width())
	}
	if approx := pos.Approx(); approx < 1.4141 || approx > 1.4144 {
		t.Errorf("positive hole approx %v, want ~1.41421...", approx)
	}
	// Sign change across the interval proves a root inside.
	if pos.poly.SignAt(pos.Lo)*pos.poly.SignAt(pos.Hi) != -1 {
		t.Errorf("no sign change over %v", &pos)
	}
	neg := s.Holes[0]
	neg.RefineTo(big.NewRat(1, 10000))
	if approx := neg.Approx(); approx < -1.4144 || approx > -1.4141 {
		t.Errorf("negative hole approx %v, want ~-1.41421...", approx)
	}
	if len(s.Poles) != 1 {
		t.Fatalf("poles = %+v, want one pole at 1", s.Poles)
	}
	rootNear(t, []IsolatedRoot{s.Poles[0].Root}, 1)
}

func TestEquivalentModes(t *testing.T) {
	// f has an extra hole at 1 relative to g.
	f := RationalFunction{
		Num: PolyFromInts(-1, 1),    // x-1
		Den: PolyFromInts(2, -3, 1), // (x-1)(x-2)
	}
	g := RationalFunction{
		Num: PolyFromInts(1),
		Den: PolyFromInts(-2, 1), // x-2
	}
	if !Equivalent(f, g, CommonDomain) {
		t.Error("f and g should agree on the common domain")
	}
	if Equivalent(f, g, SameDomain) {
		t.Error("f and g must differ under SameDomain: f excludes x=1")
	}

	// Same function written with a common factor everywhere: same domain.
	h := RationalFunction{
		Num: PolyFromInts(-2, 2),    // 2(x-1)
		Den: PolyFromInts(4, -6, 2), // 2(x-1)(x-2)
	}
	if !Equivalent(f, h, SameDomain) {
		t.Error("f and h should be equivalent under SameDomain")
	}

	// Different multiplicities, same excluded set: domains are sets.
	u := RationalFunction{Num: PolyFromInts(1), Den: PolyFromInts(2, -3, 1)}
	v := RationalFunction{
		Num: PolyFromInts(-1, 1),                              // x-1
		Den: Mul(PolyFromInts(2, -3, 1), PolyFromInts(-1, 1)), // (x-1)^2(x-2)
	}
	if !Equivalent(u, v, SameDomain) {
		t.Error("u and v share the excluded set {1,2}: want SameDomain equal")
	}
}

func TestEquivalentZeroNumerator(t *testing.T) {
	a := RationalFunction{Num: Zero(), Den: PolyFromInts(-1, 1)} // 0/(x-1)
	b := RationalFunction{Num: Zero(), Den: PolyFromInts(-2, 1)} // 0/(x-2)
	c := RationalFunction{Num: Zero(), Den: PolyFromInts(2, -2)} // 0/(2-2x)
	if !Equivalent(a, b, CommonDomain) {
		t.Error("zero functions should agree on the common domain")
	}
	if Equivalent(a, b, SameDomain) {
		t.Error("0/(x-1) and 0/(x-2) have different domains")
	}
	if !Equivalent(a, c, SameDomain) {
		t.Error("0/(x-1) and 0/(2-2x) have the same domain")
	}
}

func TestZeroDenominatorRejected(t *testing.T) {
	if _, err := Simplify(RationalFunction{Num: PolyFromInts(1), Den: Zero()}); err == nil {
		t.Error("Simplify with zero denominator should fail")
	}
	if Equivalent(RationalFunction{Num: PolyFromInts(1), Den: Zero()},
		RationalFunction{Num: PolyFromInts(1), Den: PolyFromInts(1)}, CommonDomain) {
		t.Error("zero denominator must never be equivalent")
	}
}
