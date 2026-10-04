package piecewise

import (
	"math/big"
	"testing"
)

func rat(n, d int64) *big.Rat { return big.NewRat(n, d) }

func assertRat(t *testing.T, got *big.Rat, wantN, wantD int64, what string) {
	t.Helper()
	if got == nil || got.Cmp(rat(wantN, wantD)) != 0 {
		t.Errorf("%s: got %v, want %d/%d", what, got, wantN, wantD)
	}
}

func TestPolyEvalDerivIntegral(t *testing.T) {
	p := PolyOf(1, 2, 3) // 1 + 2x + 3x^2
	assertRat(t, p.Eval(rat(1, 2)), 11, 4, "p(1/2)")
	assertRat(t, p.Deriv().Eval(rat(1, 2)), 5, 1, "p'(1/2)")
	assertRat(t, p.Integral(rat(0, 1), rat(1, 1)), 3, 1, "int_0^1 p")
	assertRat(t, p.Integral(rat(1, 3), rat(1, 2)), 85, 216, "int_{1/3}^{1/2} p")
	// reversed / zero-length bounds
	assertRat(t, p.Integral(rat(1, 1), rat(0, 1)), -3, 1, "reversed integral")
	assertRat(t, p.Integral(rat(2, 1), rat(2, 1)), 0, 1, "zero-length integral")
}

func TestPolyEqualAndDegree(t *testing.T) {
	if !NewPoly(rat(1, 1), rat(0, 1), nil).Equal(PolyOf(1)) {
		t.Error("trailing zeros must be ignored")
	}
	if (Poly{}).Degree() != -1 || PolyOf(0).Degree() != -1 || PolyOf(1, 2).Degree() != 1 {
		t.Error("bad degrees")
	}
	if PolyOf(1).Equal(PolyOf(1, 1)) {
		t.Error("unequal polys reported equal")
	}
}

func TestReplacePreservesOldVersions(t *testing.T) {
	v1 := New().Replace(rat(0, 1), rat(10, 1), PolyOf(1))
	v2 := v1.Replace(rat(2, 1), rat(4, 1), PolyOf(9))

	if v1.NumPieces() != 1 || v2.NumPieces() != 3 {
		t.Fatalf("piece counts: v1=%d v2=%d", v1.NumPieces(), v2.NumPieces())
	}
	if got, _ := v1.Eval(rat(3, 1)); got.Cmp(rat(1, 1)) != 0 {
		t.Errorf("old version mutated: v1(3)=%v", got)
	}
	if got, _ := v2.Eval(rat(3, 1)); got.Cmp(rat(9, 1)) != 0 {
		t.Errorf("v2(3)=%v, want 9", got)
	}
	// integral of old version unchanged
	assertRat(t, v1.Integral(rat(0, 1), rat(10, 1)), 10, 1, "v1 integral")
	assertRat(t, v2.Integral(rat(0, 1), rat(10, 1)), 26, 1, "v2 integral")
}

func TestReplaceClipsOverlappingPieces(t *testing.T) {
	f := New().Replace(rat(0, 1), rat(10, 1), PolyOf(0, 1)) // f(x)=x on [0,10)
	g := f.Replace(rat(2, 1), rat(4, 1), PolyOf(0))         // zero out [2,4)

	if g.NumPieces() != 3 {
		t.Fatalf("got %d pieces, want 3", g.NumPieces())
	}
	for x, want := range map[int64]int64{1: 1, 3: 0, 5: 5, 9: 9} {
		got, ok := g.Eval(rat(x, 1))
		if !ok || got.Cmp(rat(want, 1)) != 0 {
			t.Errorf("g(%d)=%v ok=%v, want %d", x, got, ok, want)
		}
	}
	// replacing across everything collapses back to one piece
	h := g.Replace(rat(-5, 1), rat(20, 1), PolyOf(2))
	if h.NumPieces() != 1 {
		t.Fatalf("got %d pieces, want 1", h.NumPieces())
	}
	assertRat(t, h.Integral(rat(0, 1), rat(10, 1)), 20, 1, "h integral")
}

func TestReplaceMergesIdenticalAdjacentPieces(t *testing.T) {
	f := New().Replace(rat(0, 1), rat(2, 1), PolyOf(5))
	f = f.Replace(rat(1, 1), rat(3, 1), PolyOf(5))
	if f.NumPieces() != 1 {
		t.Fatalf("identical adjacent pieces not merged: %d pieces", f.NumPieces())
	}
	lo, hi, _ := f.Piece(0)
	if lo.Cmp(rat(0, 1)) != 0 || hi.Cmp(rat(3, 1)) != 0 {
		t.Errorf("merged piece = [%v,%v), want [0,3)", lo, hi)
	}
	// derivative query works across the former seam
	if _, ok := f.Deriv(rat(1, 1)); !ok {
		t.Error("Deriv at merged seam should be defined")
	}
}

func TestDegenerateReplaceIsNoOp(t *testing.T) {
	f := New().Replace(rat(0, 1), rat(2, 1), PolyOf(1))
	if g := f.Replace(rat(1, 1), rat(1, 1), PolyOf(7)); g != f {
		t.Error("zero-length replace should return the same version")
	}
	if g := f.Replace(rat(2, 1), rat(1, 1), PolyOf(7)); g != f {
		t.Error("reversed replace should return the same version")
	}
}

// step is 1 on [0,2) and x on [2,5): a jump discontinuity at x=2.
func step() *Piecewise {
	return New().
		Replace(rat(0, 1), rat(2, 1), PolyOf(1)).
		Replace(rat(2, 1), rat(5, 1), PolyOf(0, 1))
}

func TestIntegralPerSegmentIndependence(t *testing.T) {
	f := step()
	assertRat(t, f.Integral(rat(0, 1), rat(5, 1)), 25, 2, "whole integral")
	// sum of independent per-segment integrals matches; the shared
	// breakpoint x=2 is not double counted
	a := f.Integral(rat(0, 1), rat(2, 1))
	b := f.Integral(rat(2, 1), rat(5, 1))
	assertRat(t, new(big.Rat).Add(a, b), 25, 2, "segment sum")
	// splitting inside pieces agrees too
	c := f.Integral(rat(0, 1), rat(1, 1))
	d := f.Integral(rat(1, 1), rat(3, 1))
	e := f.Integral(rat(3, 1), rat(5, 1))
	assertRat(t, new(big.Rat).Add(new(big.Rat).Add(c, d), e), 25, 2, "finer split")
}

func TestIntegralReversedAndZeroLength(t *testing.T) {
	f := step()
	assertRat(t, f.Integral(rat(5, 1), rat(0, 1)), -25, 2, "reversed bounds")
	assertRat(t, f.Integral(rat(3, 1), rat(3, 1)), 0, 1, "zero length interior")
	assertRat(t, f.Integral(rat(2, 1), rat(2, 1)), 0, 1, "zero length at breakpoint")
	assertRat(t, f.Integral(rat(7, 1), rat(7, 1)), 0, 1, "zero length outside domain")
}

func TestIntegralCoincidentBreakpoints(t *testing.T) {
	// three pieces meeting at x=1 and x=2 with different constants
	f := New().
		Replace(rat(0, 1), rat(1, 1), PolyOf(1)).
		Replace(rat(1, 1), rat(2, 1), PolyOf(2)).
		Replace(rat(2, 1), rat(3, 1), PolyOf(3))
	if f.NumPieces() != 3 {
		t.Fatalf("got %d pieces, want 3", f.NumPieces())
	}
	assertRat(t, f.Integral(rat(0, 1), rat(3, 1)), 6, 1, "integral across coincident breakpoints")
	got := new(big.Rat).Add(f.Integral(rat(0, 1), rat(1, 1)),
		new(big.Rat).Add(f.Integral(rat(1, 1), rat(2, 1)), f.Integral(rat(2, 1), rat(3, 1))))
	assertRat(t, got, 6, 1, "per-segment sum")
	// right-continuous value at the shared breakpoints
	assertRat(t, mustEval(t, f, 1), 2, 1, "f(1)")
	assertRat(t, mustEval(t, f, 2), 3, 1, "f(2)")
}

func mustEval(t *testing.T, f *Piecewise, x int64) *big.Rat {
	t.Helper()
	v, ok := f.Eval(rat(x, 1))
	if !ok {
		t.Fatalf("Eval(%d) not defined", x)
	}
	return v
}

func TestDiscontinuityQueries(t *testing.T) {
	f := step() // left value 1, right value 2 at x=2

	assertRat(t, mustEval(t, f, 2), 2, 1, "right-continuous value at jump")

	l, ok := f.EvalLeft(rat(2, 1))
	if !ok {
		t.Fatal("EvalLeft(2) undefined")
	}
	assertRat(t, l, 1, 1, "left limit at jump")
	r, ok := f.EvalRight(rat(2, 1))
	if !ok {
		t.Fatal("EvalRight(2) undefined")
	}
	assertRat(t, r, 2, 1, "right limit at jump")

	if _, ok := f.Deriv(rat(2, 1)); ok {
		t.Error("Deriv at discontinuity must report not-ok")
	}
	dl, ok := f.DerivLeft(rat(2, 1))
	if !ok {
		t.Fatal("DerivLeft(2) undefined")
	}
	assertRat(t, dl, 0, 1, "left derivative at jump")
	dr, ok := f.DerivRight(rat(2, 1))
	if !ok {
		t.Fatal("DerivRight(2) undefined")
	}
	assertRat(t, dr, 1, 1, "right derivative at jump")

	// interior points differentiate fine
	assertRat(t, mustDeriv(t, f, 1), 0, 1, "f'(1)")
	assertRat(t, mustDeriv(t, f, 3), 1, 1, "f'(3)")
}

func mustDeriv(t *testing.T, f *Piecewise, x int64) *big.Rat {
	t.Helper()
	v, ok := f.Deriv(rat(x, 1))
	if !ok {
		t.Fatalf("Deriv(%d) not defined", x)
	}
	return v
}

func TestRationalBreakpointsExact(t *testing.T) {
	// f(x) = x/2 on [1/3, 1/2)
	f := New().Replace(rat(1, 3), rat(1, 2), NewPoly(rat(0, 1), rat(1, 2)))
	assertRat(t, f.Integral(rat(1, 3), rat(1, 2)), 5, 144, "exact rational integral")
	assertRat(t, f.Integral(rat(0, 1), rat(1, 1)), 5, 144, "integral clips to domain")
	if f.Covers(rat(0, 1), rat(1, 1)) {
		t.Error("Covers should be false outside the domain")
	}
	if !f.Covers(rat(1, 3), rat(1, 2)) || !f.Covers(rat(2, 5), rat(3, 7)) {
		t.Error("Covers should be true inside the domain")
	}
	if _, ok := f.Eval(rat(1, 4)); ok {
		t.Error("Eval outside domain must report not-ok")
	}
}

func TestEmptyFunction(t *testing.T) {
	f := New()
	assertRat(t, f.Integral(rat(0, 1), rat(1, 1)), 0, 1, "integral over empty domain")
	if _, ok := f.Eval(rat(0, 1)); ok {
		t.Error("Eval on empty function must report not-ok")
	}
	if _, ok := f.Deriv(rat(0, 1)); ok {
		t.Error("Deriv on empty function must report not-ok")
	}
	if !f.Covers(rat(1, 1), rat(1, 1)) {
		t.Error("empty interval is trivially covered")
	}
}
