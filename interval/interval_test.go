package interval

import (
	"math/big"
	"testing"
)

func mustNew(t *testing.T, loNum, loDen, hiNum, hiDen int64) Interval {
	t.Helper()
	iv, err := New(Rat(loNum, loDen), Rat(hiNum, hiDen))
	if err != nil {
		t.Fatalf("New(%d/%d, %d/%d): %v", loNum, loDen, hiNum, hiDen, err)
	}
	return iv
}

func expectEq(t *testing.T, got Interval, loNum, loDen, hiNum, hiDen int64) {
	t.Helper()
	want := Must(Rat(loNum, loDen), Rat(hiNum, hiDen))
	if !got.Equal(want) {
		t.Fatalf("got %s, want %s", got, want)
	}
}

// Acceptance boundary: A=[1,2], B=[3,4] => A*B=[3,8], A/B=[1/4,2/3].
func TestAcceptanceBoundaries(t *testing.T) {
	a := mustNew(t, 1, 1, 2, 1)
	b := mustNew(t, 3, 1, 4, 1)

	expectEq(t, a.Mul(b), 3, 1, 8, 1)

	q, err := a.Div(b)
	if err != nil {
		t.Fatalf("Div: %v", err)
	}
	expectEq(t, q, 1, 4, 2, 3)
}

func TestAddSub(t *testing.T) {
	a := mustNew(t, 1, 1, 2, 1)
	b := mustNew(t, 3, 1, 4, 1)
	expectEq(t, a.Add(b), 4, 1, 6, 1)
	expectEq(t, a.Sub(b), -3, 1, -1, 1)
	expectEq(t, b.Sub(a), 1, 1, 3, 1)
}

func TestMulMixedSigns(t *testing.T) {
	a := mustNew(t, -2, 1, 3, 1)
	b := mustNew(t, -1, 1, 4, 1)
	// products: 2, -8, -3, 12 -> hull [-8, 12]
	expectEq(t, a.Mul(b), -8, 1, 12, 1)
}

func TestReciprocal(t *testing.T) {
	a := mustNew(t, 2, 1, 4, 1)
	r, err := a.Reciprocal()
	if err != nil {
		t.Fatalf("Reciprocal: %v", err)
	}
	expectEq(t, r, 1, 4, 1, 2)

	neg := mustNew(t, -4, 1, -2, 1)
	rn, err := neg.Reciprocal()
	if err != nil {
		t.Fatalf("Reciprocal: %v", err)
	}
	expectEq(t, rn, -1, 2, -1, 4)
}

// Division by an interval containing zero must raise the
// indeterminate-domain error.
func TestDivByZeroContainingInterval(t *testing.T) {
	a := mustNew(t, 1, 1, 2, 1)
	for _, d := range []Interval{
		mustNew(t, -1, 1, 1, 1),
		mustNew(t, 0, 1, 1, 1),
		mustNew(t, -1, 1, 0, 1),
		mustNew(t, 0, 1, 0, 1),
	} {
		if _, err := a.Div(d); err != ErrIndeterminateDomain {
			t.Fatalf("Div by %s: got err=%v, want ErrIndeterminateDomain", d, err)
		}
		if _, err := d.Reciprocal(); err != ErrIndeterminateDomain {
			t.Fatalf("Reciprocal of %s: got err=%v, want ErrIndeterminateDomain", d, err)
		}
	}
}

func TestSqrtPerfectSquares(t *testing.T) {
	a := mustNew(t, 1, 1, 4, 1)
	s, err := a.Sqrt()
	if err != nil {
		t.Fatalf("Sqrt: %v", err)
	}
	expectEq(t, s, 1, 1, 2, 1)
}

// Sqrt of non-square rationals must be outward rounded so the true
// (irrational) result stays inside the closed result interval.
func TestSqrtOutwardEnclosure(t *testing.T) {
	a := mustNew(t, 2, 1, 3, 1)
	s, err := a.Sqrt()
	if err != nil {
		t.Fatalf("Sqrt: %v", err)
	}
	// lo^2 <= 2 and hi^2 >= 3, with rational endpoints.
	two := big.NewRat(2, 1)
	three := big.NewRat(3, 1)
	lo2 := new(big.Rat).Mul(s.Lo, s.Lo)
	hi2 := new(big.Rat).Mul(s.Hi, s.Hi)
	if lo2.Cmp(two) > 0 {
		t.Fatalf("sqrt enclosure lower bound too high: %s^2=%s > 2", s.Lo, lo2)
	}
	if hi2.Cmp(three) < 0 {
		t.Fatalf("sqrt enclosure upper bound too low: %s^2=%s < 3", s.Hi, hi2)
	}
	if s.Width().Sign() <= 0 {
		t.Fatalf("expected non-degenerate enclosure, got %s", s)
	}
}

func TestSqrtNegativeDomain(t *testing.T) {
	a := mustNew(t, -1, 1, 4, 1)
	if _, err := a.Sqrt(); err != ErrIndeterminateDomain {
		t.Fatalf("Sqrt of %s: got err=%v, want ErrIndeterminateDomain", a, err)
	}
}

// Endpoints stay exact rationals; no float rounding may leak outside.
func TestExactRationalEndpoints(t *testing.T) {
	a := mustNew(t, 1, 3, 2, 3)
	b := mustNew(t, 1, 7, 3, 7)
	sum := a.Add(b)
	expectEq(t, sum, 10, 21, 23, 21)

	prod := a.Mul(b)
	expectEq(t, prod, 1, 21, 2, 7)

	q, err := a.Div(b)
	if err != nil {
		t.Fatalf("Div: %v", err)
	}
	// [1/3,2/3] / [1/7,3/7] = [ (1/3)/(3/7), (2/3)/(1/7) ] = [7/9, 14/3]
	expectEq(t, q, 7, 9, 14, 3)
}

func TestNewRejectsInvertedBounds(t *testing.T) {
	if _, err := New(Rat(2, 1), Rat(1, 1)); err == nil {
		t.Fatal("expected error for lo > hi")
	}
}

func TestContains(t *testing.T) {
	a := mustNew(t, 1, 2, 3, 2)
	if !a.Contains(Rat(1, 1)) || !a.Contains(Rat(1, 2)) || !a.Contains(Rat(3, 2)) {
		t.Fatalf("expected containment within %s", a)
	}
	if a.Contains(Rat(2, 1)) {
		t.Fatalf("did not expect 2 in %s", a)
	}
}
