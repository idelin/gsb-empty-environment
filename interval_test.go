package interval

import (
	"math/big"
	"testing"
)

func rat(num, den int64) *big.Rat { return big.NewRat(num, den) }

func TestContainsOpenClosed(t *testing.T) {
	iv := Make(Fin(rat(1, 1)), Fin(rat(3, 1)), true, false) // (1, 3]
	if iv.Contains(rat(1, 1)) {
		t.Error("open left endpoint must be excluded")
	}
	if !iv.Contains(rat(3, 1)) {
		t.Error("closed right endpoint must be included")
	}
	if !iv.Contains(rat(2, 1)) {
		t.Error("interior point must be included")
	}
	if iv.Contains(rat(4, 1)) || iv.Contains(rat(0, 1)) {
		t.Error("outside points must be excluded")
	}
}

func TestPointIsNotEmpty(t *testing.T) {
	p := Point(rat(-7, 3))
	if p.IsEmpty() {
		t.Fatal("single point must not be empty")
	}
	if !p.IsPoint() {
		t.Fatal("expected IsPoint")
	}
	if !p.Contains(rat(-7, 3)) || p.Contains(rat(-7, 2)) {
		t.Error("point must contain exactly itself")
	}
	// An open "point" (a, a) is empty.
	if Make(Fin(rat(1, 1)), Fin(rat(1, 1)), true, false).IsEmpty() != true {
		t.Error("open degenerate interval must be empty")
	}
}

func TestEmptySet(t *testing.T) {
	e := Empty()
	if !e.IsEmpty() {
		t.Fatal("expected empty")
	}
	if e.Contains(rat(0, 1)) || e.Contains(rat(-99, 7)) {
		t.Error("empty set contains nothing")
	}
	if !Closed(rat(2, 1), rat(1, 1)).IsEmpty() {
		t.Error("lo > hi must normalize to empty")
	}
	if got := e.Intersect(All()); !got.IsEmpty() {
		t.Error("empty ∩ all must be empty")
	}
	if got := e.Add(All()); !got.IsEmpty() {
		t.Error("empty + all must be empty")
	}
	if got := e.Mul(All()); !got.IsEmpty() {
		t.Error("empty * all must be empty")
	}
}

func TestInfiniteEndpoints(t *testing.T) {
	all := All()
	for _, v := range []*big.Rat{rat(0, 1), rat(-10, 3), rat(123456789, 2)} {
		if !all.Contains(v) {
			t.Errorf("all must contain %v", v)
		}
	}
	left := Make(NegInf(), Fin(rat(0, 1)), true, false) // (-inf, 0]
	if !left.Contains(rat(-1000000, 1)) || !left.Contains(rat(0, 1)) || left.Contains(rat(1, 2)) {
		t.Error("(-inf, 0] containment wrong")
	}
	if !left.loOpen {
		t.Error("infinite endpoint must be forced open")
	}
}

func TestIntersect(t *testing.T) {
	a := Closed(rat(-2, 1), rat(2, 1))
	b := Make(Fin(rat(0, 1)), Fin(rat(5, 1)), true, true) // (0, 5)
	got := a.Intersect(b)
	if got.Contains(rat(0, 1)) || !got.Contains(rat(2, 1)) || got.Contains(rat(3, 1)) {
		t.Errorf("intersect = %v, want (0, 2]", got)
	}
	if !a.Intersect(Closed(rat(3, 1), rat(4, 1))).IsEmpty() {
		t.Error("disjoint intersect must be empty")
	}
}

func TestAddSubNegatives(t *testing.T) {
	a := Closed(rat(-5, 1), rat(-1, 1))
	b := Closed(rat(-3, 1), rat(2, 1))
	sum := a.Add(b) // [-8, 1]
	if !sum.Contains(rat(-8, 1)) || !sum.Contains(rat(1, 1)) || sum.Contains(rat(2, 1)) {
		t.Errorf("sum = %v, want [-8, 1]", sum)
	}
	diff := a.Sub(b) // [-7, 2]
	if !diff.Contains(rat(-7, 1)) || !diff.Contains(rat(2, 1)) || diff.Contains(rat(3, 1)) {
		t.Errorf("diff = %v, want [-7, 2]", diff)
	}
	neg := a.Neg() // [1, 5]
	if !neg.Contains(rat(1, 1)) || !neg.Contains(rat(5, 1)) || neg.Contains(rat(0, 1)) {
		t.Errorf("neg = %v, want [1, 5]", neg)
	}
}

func TestMulSigns(t *testing.T) {
	cases := []struct {
		a, b Interval
		in   []*big.Rat
		out  []*big.Rat
	}{
		{Closed(rat(-2, 1), rat(3, 1)), Closed(rat(-4, 1), rat(5, 1)),
			[]*big.Rat{rat(-12, 1), rat(15, 1), rat(0, 1)}, []*big.Rat{rat(16, 1), rat(-13, 1)}},
		{Closed(rat(-2, 1), rat(-1, 1)), Closed(rat(-4, 1), rat(-3, 1)),
			[]*big.Rat{rat(3, 1), rat(8, 1)}, []*big.Rat{rat(2, 1), rat(9, 1)}},
		{Closed(rat(0, 1), rat(1, 1)), Make(Fin(rat(1, 1)), PosInf(), false, true),
			[]*big.Rat{rat(0, 1), rat(7, 1)}, []*big.Rat{rat(-1, 10)}},
	}
	for k, c := range cases {
		got := c.a.Mul(c.b)
		for _, v := range c.in {
			if !got.Contains(v) {
				t.Errorf("case %d: %v must contain %v", k, got, v)
			}
		}
		for _, v := range c.out {
			if got.Contains(v) {
				t.Errorf("case %d: %v must not contain %v", k, got, v)
			}
		}
	}
	// {0} * (-inf, +inf) = {0}
	if got := Point(rat(0, 1)).Mul(All()); !got.IsPoint() || !got.Contains(rat(0, 1)) {
		t.Errorf("0 * all = %v, want {0}", got)
	}
}

func TestDivAcrossZero(t *testing.T) {
	// [1, 2] / [-1, 1] = (-inf, -1] ∪ [1, +inf)
	got := Closed(rat(1, 1), rat(2, 1)).Div(Closed(rat(-1, 1), rat(1, 1)))
	if len(got.Segments) != 2 {
		t.Fatalf("expected 2 segments, got %v", got)
	}
	for _, v := range []*big.Rat{rat(1, 1), rat(2, 1), rat(-1, 1), rat(-2, 1), rat(100, 1), rat(-100, 1)} {
		if !got.Contains(v) {
			t.Errorf("%v must contain %v", got, v)
		}
	}
	for _, v := range []*big.Rat{rat(0, 1), rat(1, 2), rat(-1, 2), rat(9, 10)} {
		if got.Contains(v) {
			t.Errorf("%v must not contain %v", got, v)
		}
	}
}

func TestDivZeroNumeratorAndDenominator(t *testing.T) {
	// [-1, 1] / [-1, 1] = (-inf, +inf): any real is a/b for some a, b.
	got := Closed(rat(-1, 1), rat(1, 1)).Div(Closed(rat(-1, 1), rat(1, 1)))
	if len(got.Segments) != 1 || !got.Contains(rat(1000, 1)) || !got.Contains(rat(-1000, 1)) || !got.Contains(rat(0, 1)) {
		t.Errorf("got %v, want whole real line", got)
	}
	// Division by {0} is undefined: empty result.
	if got := Closed(rat(1, 1), rat(2, 1)).Div(Point(rat(0, 1))); !got.IsEmpty() {
		t.Errorf("x / {0} = %v, want empty", got)
	}
	// [2, 4] / [2, 4] = [1/2, 2]
	got2 := Closed(rat(2, 1), rat(4, 1)).Div(Closed(rat(2, 1), rat(4, 1)))
	if !got2.Contains(rat(1, 2)) || !got2.Contains(rat(2, 1)) || got2.Contains(rat(3, 1)) || got2.Contains(rat(1, 3)) {
		t.Errorf("got %v, want [1/2, 2]", got2)
	}
	// 1 / (0, 2] = [1/2, +inf)
	got3 := Point(rat(1, 1)).Div(Make(Fin(rat(0, 1)), Fin(rat(2, 1)), true, false))
	if !got3.Contains(rat(1, 2)) || !got3.Contains(rat(10, 1)) || got3.Contains(rat(1, 3)) {
		t.Errorf("got %v, want [1/2, +inf)", got3)
	}
}
