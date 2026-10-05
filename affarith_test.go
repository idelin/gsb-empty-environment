package affarith

import (
	"math"
	"testing"
)

// x - x must be exactly zero: the shared noise symbol cancels instead of
// doubling the width as independent interval arithmetic would.
func TestSubSelfDependency(t *testing.T) {
	e := NewEngine(0)
	x := e.Var(1, 3)
	z := x.Sub(x)
	iv := z.Interval()
	if iv.Lo != 0 || iv.Hi != 0 {
		t.Fatalf("x - x = [%v, %v], want exactly [0, 0]", iv.Lo, iv.Hi)
	}
	if z.NumNoise() != 0 {
		t.Fatalf("x - x keeps %d noise symbols, want 0", z.NumNoise())
	}
	// Independent interval arithmetic cannot see the cancellation.
	naive := evalInterval(z.Node(), []Interval{{1, 3}})
	if naive != (Interval{-2, 2}) {
		t.Fatalf("naive interval for x - x = %v, want [-2, 2]", naive)
	}
}

// Shared subexpressions: y - y with y = x+1 is zero, and x*(1-x) keeps the
// negative correlation between the factors, beating naive intervals.
func TestSharedSubexpression(t *testing.T) {
	e := NewEngine(0)
	x := e.Var(0, 1)
	y := x.Add(e.Const(1))
	z := y.Sub(y)
	if iv := z.Interval(); iv.Lo != 0 || iv.Hi != 0 {
		t.Fatalf("(x+1) - (x+1) = [%v, %v], want [0, 0]", iv.Lo, iv.Hi)
	}

	// f = x*(1-x) on [0,1]; true range [0, 1/4].
	f := x.Mul(e.Const(1).Sub(x))
	iv := f.Interval()
	if iv.Lo > 0 || iv.Lo < -1e-6 {
		t.Fatalf("x*(1-x) lower bound %v, want in [-1e-6, 0]", iv.Lo)
	}
	if iv.Hi < 0.25 || iv.Hi > 0.5+1e-9 {
		t.Fatalf("x*(1-x) upper bound %v, want in [0.25, 0.5]", iv.Hi)
	}
	naive := evalInterval(f.Node(), []Interval{{0, 1}})
	if naive != (Interval{0, 1}) {
		t.Fatalf("naive interval for x*(1-x) = %v, want [0, 1]", naive)
	}
	if iv.Hi >= naive.Hi {
		t.Fatalf("affine bound %v not tighter than naive %v", iv, naive)
	}
}

// Multiplication of two uncertain quantities adds exactly one remainder
// symbol, and the enclosure is conservative.
func TestMulRemainder(t *testing.T) {
	e := NewEngine(0)
	x := e.Var(-1, 2)
	y := e.Var(0, 3)
	p := x.Mul(y)
	if p.NumNoise() != 3 {
		t.Fatalf("x*y has %d noise symbols, want 3 (2 inputs + 1 remainder)", p.NumNoise())
	}
	cert := e.Certify(p, 3)
	if err := Verify(cert, p.Node()); err != nil {
		t.Fatalf("certificate for x*y rejected: %v", err)
	}
}

// When the noise-symbol limit is exceeded, symbols are merged
// conservatively: the count stays within the limit and the enclosure still
// contains the unlimited-precision enclosure.
func TestNoiseCompactionConservative(t *testing.T) {
	build := func(e *Engine) Affine {
		x := e.Var(0, 1)
		y := e.Var(-1, 1)
		z := e.Var(2, 4)
		return x.Mul(y).Add(z.Mul(x)).Add(y.Mul(z))
	}

	full := build(NewEngine(0))
	limited := build(NewEngine(2))
	if limited.NumNoise() > 2 {
		t.Fatalf("compacted form has %d noise symbols, limit is 2", limited.NumNoise())
	}
	ivFull, ivLim := full.Interval(), limited.Interval()
	if ivLim.Lo > ivFull.Lo+1e-9 || ivLim.Hi < ivFull.Hi-1e-9 {
		t.Fatalf("compacted %v does not contain uncompacted %v", ivLim, ivFull)
	}

	// The compacted result must still certify against exact evaluation.
	cert := limited.eng.Certify(limited, 3)
	if err := Verify(cert, limited.Node()); err != nil {
		t.Fatalf("certificate for compacted form rejected: %v", err)
	}
}

// Division by a form whose range crosses zero is explicitly rejected, and
// DivPieces returns the two one-sided pieces with unbounded ranges.
func TestDivCrossingZero(t *testing.T) {
	e := NewEngine(0)
	x := e.Var(-1, 1)
	one := e.Const(1)

	_, err := one.Div(x)
	if err == nil {
		t.Fatal("1/x with x in [-1,1] must be rejected")
	}
	zce, ok := err.(*ZeroCrossingError)
	if !ok {
		t.Fatalf("error type = %T, want *ZeroCrossingError", err)
	}
	if !(zce.Divisor.Lo <= 0 && 0 <= zce.Divisor.Hi) {
		t.Fatalf("reported divisor range %v does not cross zero", zce.Divisor)
	}

	pieces := DivPieces(one, x)
	if len(pieces) != 2 {
		t.Fatalf("got %d pieces, want 2", len(pieces))
	}
	neg, pos := pieces[0], pieces[1]
	approx := func(v, want float64) bool { return math.Abs(v-want) < 1e-9 }
	if !approx(neg.DivisorDomain.Lo, -1) || neg.DivisorDomain.Hi != 0 ||
		pos.DivisorDomain.Lo != 0 || !approx(pos.DivisorDomain.Hi, 1) {
		t.Fatalf("piece domains %v, %v", neg.DivisorDomain, pos.DivisorDomain)
	}
	if !math.IsInf(neg.Range.Lo, -1) || !approx(neg.Range.Hi, -1) {
		t.Fatalf("negative piece range %v, want [-Inf, -1]", neg.Range)
	}
	if !approx(pos.Range.Lo, 1) || !math.IsInf(pos.Range.Hi, 1) {
		t.Fatalf("positive piece range %v, want [1, +Inf]", pos.Range)
	}
}

// Well-defined division uses a Chebyshev-linearized reciprocal and stays
// conservative and certifiable.
func TestDivisionBounds(t *testing.T) {
	e := NewEngine(0)
	x := e.Var(1, 3)
	q, err := e.Const(2).Div(x)
	if err != nil {
		t.Fatalf("2/x with x in [1,3] rejected: %v", err)
	}
	iv := q.Interval()
	if iv.Lo > 2.0/3.0 || iv.Hi < 2 {
		t.Fatalf("2/x enclosure %v does not contain true range [2/3, 2]", iv)
	}
	if iv.Hi > 2.01 {
		t.Fatalf("2/x upper bound %v, want close to 2", iv.Hi)
	}
	cert := e.Certify(q, 3)
	if err := Verify(cert, q.Node()); err != nil {
		t.Fatalf("certificate for 2/x rejected: %v", err)
	}
}

// A certificate verifies, and tampered bounds are rejected in both
// directions (unsound narrowing and over-wide claims).
func TestCertificateVerification(t *testing.T) {
	e := NewEngine(0)
	x := e.Var(0, 1)
	y := e.Var(-1, 1)
	f := x.Mul(e.Const(1).Sub(x)).Add(y.Mul(y))

	cert := e.Certify(f, 3)
	if err := Verify(cert, f.Node()); err != nil {
		t.Fatalf("valid certificate rejected: %v", err)
	}

	narrowed := cert
	narrowed.Upper -= 0.5
	if err := Verify(narrowed, f.Node()); err == nil {
		t.Fatal("certificate with narrowed upper bound accepted")
	}

	// f >= 0 on the domain (min 0 at x=0, y=0), so a lower bound
	// raised above 0 is unsound and must be rejected.
	raised := cert
	raised.Lower = 0.1
	if err := Verify(raised, f.Node()); err == nil {
		t.Fatal("certificate with raised lower bound accepted")
	}

	// Looser (still sound) bounds verify fine: soundness is checked,
	// not tightness.
	loose := cert
	loose.Lower -= 1
	loose.Upper += 1
	if err := Verify(loose, f.Node()); err != nil {
		t.Fatalf("sound but looser certificate rejected: %v", err)
	}
}
