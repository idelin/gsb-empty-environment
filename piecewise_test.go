package piecewise

import (
	"math/big"
	"testing"
)

func rat(v int64) *big.Rat { return big.NewRat(v, 1) }

func rat2(n, d int64) *big.Rat { return big.NewRat(n, d) }

func mustReplace(t *testing.T, f *Function, lo, hi *big.Rat, p Poly, loVal, hiVal *big.Rat) *Function {
	t.Helper()
	g, err := f.Replace(lo, hi, p, loVal, hiVal)
	if err != nil {
		t.Fatalf("Replace(%v,%v): %v", lo, hi, err)
	}
	return g
}

func eqRat(t *testing.T, got *big.Rat, want *big.Rat, what string) {
	t.Helper()
	if got == nil || got.Cmp(want) != 0 {
		t.Fatalf("%s: got %v, want %v", what, got, want)
	}
}

// build f(x) = x on [0,1], f(x) = x^2 on [1,3] (coincident breakpoint at 1).
func twoPiece(t *testing.T) *Function {
	t.Helper()
	f := New()
	f = mustReplace(t, f, rat(0), rat(1), PolyInts(0, 1), nil, nil)
	f = mustReplace(t, f, rat(1), rat(3), PolyInts(0, 0, 1), nil, nil)
	return f
}

func TestIntegrateExact(t *testing.T) {
	f := twoPiece(t)
	// ∫0^1 x dx = 1/2 ; ∫1^3 x^2 dx = (27-1)/3 = 26/3 ; total = 55/6
	eqRat(t, f.Integrate(rat(0), rat(3)), rat2(55, 6), "whole")
	eqRat(t, f.Integrate(rat(0), rat(1)), rat2(1, 2), "first piece")
	eqRat(t, f.Integrate(rat(1), rat(3)), rat2(26, 3), "second piece")
	// Partial piece: ∫0^(1/2) x dx = 1/8
	eqRat(t, f.Integrate(rat(0), rat2(1, 2)), rat2(1, 8), "partial")
}

func TestIntegrateReversedAndZeroLength(t *testing.T) {
	f := twoPiece(t)
	fwd := f.Integrate(rat(0), rat(3))
	rev := f.Integrate(rat(3), rat(0))
	if new(big.Rat).Add(fwd, rev).Sign() != 0 {
		t.Fatalf("reversed interval: %v + %v != 0", fwd, rev)
	}
	if got := f.Integrate(rat(1), rat(1)); got.Sign() != 0 {
		t.Fatalf("zero-length interval: got %v, want 0", got)
	}
	// Zero-length at a breakpoint with distinct point values must still be 0.
	g := New()
	g = mustReplace(t, g, rat(0), rat(1), PolyInts(0, 1), rat(5), rat(7))
	if got := g.Integrate(rat(0), rat(0)); got.Sign() != 0 {
		t.Fatalf("zero-length at breakpoint: got %v, want 0", got)
	}
}

func TestIndependentPerPieceIntegration(t *testing.T) {
	f := twoPiece(t)
	// Integrating each segment independently and summing must equal the
	// whole-interval integral; the shared breakpoint is not double counted.
	sum := new(big.Rat)
	for _, s := range f.Segments() {
		sum.Add(sum, s.P.Integrate(s.Lo, s.Hi))
	}
	if sum.Cmp(f.Integrate(rat(0), rat(3))) != 0 {
		t.Fatalf("per-piece sum %v != whole %v", sum, f.Integrate(rat(0), rat(3)))
	}
	// Splitting an interval at an interior point of a piece is consistent.
	a := f.Integrate(rat2(1, 2), rat(2))
	b := new(big.Rat).Add(f.Integrate(rat2(1, 2), rat(1)), f.Integrate(rat(1), rat(2)))
	if a.Cmp(b) != 0 {
		t.Fatalf("split at breakpoint: %v != %v", a, b)
	}
}

func TestBreakpointPointValuesDoNotAffectIntegral(t *testing.T) {
	f := New()
	f = mustReplace(t, f, rat(0), rat(1), PolyInts(0, 1), nil, nil)
	g := mustReplace(t, f, rat(0), rat(1), PolyInts(0, 1), rat(100), rat(-100))
	if f.Integrate(rat(0), rat(1)).Cmp(g.Integrate(rat(0), rat(1))) != 0 {
		t.Fatal("point values changed the integral")
	}
	v, ok := g.ValueAt(rat(0))
	if !ok || v.Cmp(rat(100)) != 0 {
		t.Fatalf("ValueAt(0): got %v,%v want 100,true", v, ok)
	}
	v, _ = g.ValueAt(rat(1))
	if v.Cmp(rat(-100)) != 0 {
		t.Fatalf("ValueAt(1): got %v want -100", v)
	}
	v, _ = g.ValueAt(rat2(1, 2))
	if v.Cmp(rat2(1, 2)) != 0 {
		t.Fatalf("ValueAt(1/2): got %v want 1/2", v)
	}
}

func TestCoincidentBreakpointValue(t *testing.T) {
	// Left piece ends at 1 with HiVal=10; right piece starts at 1 with LoVal=20.
	f := New()
	f = mustReplace(t, f, rat(0), rat(1), PolyInts(0, 1), nil, rat(10))
	f = mustReplace(t, f, rat(1), rat(2), PolyInts(0, 2), rat(20), nil)
	if v, _ := f.ValueAt(rat(1)); v.Cmp(rat(20)) != 0 {
		t.Fatalf("ValueAt(1): got %v, want right value 20", v)
	}
}

func TestDerivativeAtDiscontinuity(t *testing.T) {
	// Jump at x=1: f = x on [0,1], f = x+5 on [1,2].
	f := New()
	f = mustReplace(t, f, rat(0), rat(1), PolyInts(0, 1), nil, nil)
	f = mustReplace(t, f, rat(1), rat(2), PolyInts(5, 1), nil, nil)
	d := f.DerivativeAt(rat(1))
	if d.Exists {
		t.Fatal("derivative should not exist at a jump")
	}
	if d.Continuous {
		t.Fatal("function should not be continuous at a jump")
	}
	eqRat(t, d.Left, rat(1), "left derivative")
	eqRat(t, d.Right, rat(1), "right derivative")
	// Interior point.
	d = f.DerivativeAt(rat2(1, 2))
	if !d.Exists || d.Value.Cmp(rat(1)) != 0 {
		t.Fatalf("interior derivative: %+v", d)
	}
}

func TestDerivativeAtKinkAndSmoothJoin(t *testing.T) {
	// Kink at x=1: f = x on [0,1], f = 2x-1 on [1,2] (continuous, slopes 1 and 2).
	kink := New()
	kink = mustReplace(t, kink, rat(0), rat(1), PolyInts(0, 1), nil, nil)
	kink = mustReplace(t, kink, rat(1), rat(2), PolyInts(-1, 2), nil, nil)
	d := kink.DerivativeAt(rat(1))
	if d.Exists || !d.Continuous {
		t.Fatalf("kink: want continuous but no derivative, got %+v", d)
	}
	eqRat(t, d.Left, rat(1), "kink left")
	eqRat(t, d.Right, rat(2), "kink right")

	// Smooth join at x=1: f = x^2 on [0,1], f = 2x-1 on [1,2].
	smooth := New()
	smooth = mustReplace(t, smooth, rat(0), rat(1), PolyInts(0, 0, 1), nil, nil)
	smooth = mustReplace(t, smooth, rat(1), rat(2), PolyInts(-1, 2), nil, nil)
	d = smooth.DerivativeAt(rat(1))
	if !d.Exists || d.Value.Cmp(rat(2)) != 0 {
		t.Fatalf("smooth join: want derivative 2, got %+v", d)
	}
}

func TestReplaceKeepsOldVersion(t *testing.T) {
	f := twoPiece(t)
	g := mustReplace(t, f, rat2(1, 2), rat2(3, 2), PolyInts(1), nil, nil)
	// Old version unchanged.
	eqRat(t, f.Integrate(rat(0), rat(3)), rat2(55, 6), "old version")
	// New version: ∫0^(1/2) x dx + ∫(1/2)^(3/2) 1 dx + ∫(3/2)^3 x^2 dx
	// = 1/8 + 1 + (9 - 9/8) = 9
	eqRat(t, g.Integrate(rat(0), rat(3)), rat(9), "new version")
	if len(g.Segments()) != 3 {
		t.Fatalf("new version segments: got %d, want 3", len(g.Segments()))
	}
	// Mutating g again must not disturb f.
	h := mustReplace(t, g, rat(0), rat(3), PolyInts(0), nil, nil)
	eqRat(t, h.Integrate(rat(0), rat(3)), rat(0), "zero function")
	eqRat(t, f.Integrate(rat(0), rat(3)), rat2(55, 6), "old version still intact")
}

func TestReplaceRejectsEmptyInterval(t *testing.T) {
	f := New()
	if _, err := f.Replace(rat(1), rat(1), PolyInts(1), nil, nil); err != ErrEmptyInterval {
		t.Fatalf("zero-length replace: got %v", err)
	}
	if _, err := f.Replace(rat(2), rat(1), PolyInts(1), nil, nil); err != ErrEmptyInterval {
		t.Fatalf("reversed replace: got %v", err)
	}
}

func TestDerivPieces(t *testing.T) {
	f := twoPiece(t)
	d := f.Deriv()
	// Derivative pieces: 1 on [0,1], 2x on [1,3].
	eqRat(t, d.Integrate(rat(0), rat(1)), rat(1), "deriv piece 1")
	eqRat(t, d.Integrate(rat(1), rat(3)), rat(8), "deriv piece 2")
	// Fundamental theorem per piece: ∫f' = f(3-) - f(1+) on second piece.
	v3, _ := f.ValueAt(rat(3))
	v1, _ := f.ValueAt(rat(1))
	want := new(big.Rat).Sub(v3, v1)
	if got := d.Integrate(rat(1), rat(3)); got.Cmp(want) != 0 {
		t.Fatalf("FTC: got %v, want %v", got, want)
	}
}
