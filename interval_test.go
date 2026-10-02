package interval

import (
	"math/big"
	"testing"
)

func rat(t *testing.T, s string) *big.Rat {
	t.Helper()
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		t.Fatalf("bad rational literal %q", s)
	}
	return r
}

func mustEq(t *testing.T, got Interval, lo, hi string) {
	t.Helper()
	want := New(rat(t, lo), rat(t, hi))
	if !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// Acceptance: A=[1,2], B=[3,4] => A*B=[3,8].
func TestMulAcceptance(t *testing.T) {
	a, b := OfInt64(1, 2), OfInt64(3, 4)
	mustEq(t, a.Mul(b), "3", "8")
}

// Acceptance: A=[1,2], B=[3,4] => A/B=[1/4, 2/3].
func TestDivAcceptance(t *testing.T) {
	a, b := OfInt64(1, 2), OfInt64(3, 4)
	q, err := a.Div(b)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	mustEq(t, q, "1/4", "2/3")
}

func TestAddSub(t *testing.T) {
	a, b := OfInt64(1, 2), OfInt64(3, 4)
	mustEq(t, a.Add(b), "4", "6")
	mustEq(t, a.Sub(b), "-3", "-1")
	mustEq(t, b.Sub(a), "1", "3")
}

func TestMulMixedSigns(t *testing.T) {
	mustEq(t, OfInt64(-2, 3).Mul(OfInt64(-4, 5)), "-12", "15")
	mustEq(t, OfInt64(-2, -1).Mul(OfInt64(3, 4)), "-8", "-3")
	mustEq(t, OfInt64(-1, 1).Mul(OfInt64(-1, 1)), "-1", "1")
}

// Acceptance: division by an interval containing 0 must raise the
// unlocalizable error.
func TestDivByZeroContainingInterval(t *testing.T) {
	a := OfInt64(1, 2)
	for _, d := range []Interval{
		OfInt64(-1, 1),
		OfInt64(0, 3),
		OfInt64(-3, 0),
		OfInt64(0, 0),
	} {
		if _, err := a.Div(d); err != ErrZeroDivisor {
			t.Fatalf("Div by %v: got err %v, want ErrZeroDivisor", d, err)
		}
		if _, err := d.Recip(); err != ErrZeroDivisor {
			t.Fatalf("Recip of %v: got err %v, want ErrZeroDivisor", d, err)
		}
	}
}

func TestRecip(t *testing.T) {
	r, err := OfInt64(3, 4).Recip()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	mustEq(t, r, "1/4", "1/3")
}

// Sqrt of a perfect rational square is exact.
func TestSqrtPerfectSquare(t *testing.T) {
	s, err := New(rat(t, "4/9"), rat(t, "25/16")).Sqrt()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	mustEq(t, s, "2/3", "5/4")
}

// Sqrt of a non-square must strictly bracket the irrational root:
// Lo^2 <= x <= Hi^2 with rational endpoints, and Lo < Hi.
func TestSqrtOutwardEnclosure(t *testing.T) {
	s, err := OfInt64(2, 3).Sqrt()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	two, three := big.NewRat(2, 1), big.NewRat(3, 1)
	lo2 := new(big.Rat).Mul(s.Lo, s.Lo)
	hi2 := new(big.Rat).Mul(s.Hi, s.Hi)
	if lo2.Cmp(two) > 0 {
		t.Fatalf("Lo^2=%s exceeds 2: enclosure violated below", lo2)
	}
	if hi2.Cmp(three) < 0 {
		t.Fatalf("Hi^2=%s below 3: enclosure violated above", hi2)
	}
	if s.Lo.Cmp(s.Hi) >= 0 {
		t.Fatalf("expected strict outward rounding, got %v", s)
	}
	// Endpoints must stay tight: the gap is at most 1/q where q is the
	// operand's denominator (q=1 here, so width <= 1).
	if s.Width().Cmp(big.NewRat(1, 1)) > 0 {
		t.Fatalf("enclosure too loose: width %s", s.Width())
	}

	// With denominator q=2 the outward gap tightens to <= 1/2.
	half, err := New(rat(t, "1/2"), rat(t, "1/2")).Sqrt()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	hlo2 := new(big.Rat).Mul(half.Lo, half.Lo)
	hhi2 := new(big.Rat).Mul(half.Hi, half.Hi)
	if hlo2.Cmp(big.NewRat(1, 2)) > 0 || hhi2.Cmp(big.NewRat(1, 2)) < 0 {
		t.Fatalf("sqrt(1/2) enclosure violated: %v", half)
	}
	if half.Width().Cmp(big.NewRat(1, 2)) > 0 {
		t.Fatalf("sqrt(1/2) enclosure too loose: width %s", half.Width())
	}
}

func TestSqrtNegative(t *testing.T) {
	if _, err := OfInt64(-1, 4).Sqrt(); err != ErrNegativeRadicand {
		t.Fatalf("got err %v, want ErrNegativeRadicand", err)
	}
}

func TestSqrtZero(t *testing.T) {
	s, err := OfInt64(0, 0).Sqrt()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	mustEq(t, s, "0", "0")
}

// Endpoints stay exact rationals: 1/3 + 1/6 must be exactly 1/2, with no
// floating-point spillover, across a long accumulation chain.
func TestExactRationalEndpoints(t *testing.T) {
	sum := OfInt64(0, 0)
	step := New(rat(t, "1/3"), rat(t, "1/3"))
	for i := 0; i < 300; i++ {
		sum = sum.Add(step)
	}
	mustEq(t, sum, "100", "100")

	// (1/3) * 3 == 1 exactly, no 0.999... drift.
	p := New(rat(t, "1/3"), rat(t, "1/3")).Mul(OfInt64(3, 3))
	mustEq(t, p, "1", "1")
}

// Randomized containment check: for sampled x in a, y in b, every
// operation's true result must lie inside the computed interval.
func TestContainmentProperty(t *testing.T) {
	a := New(rat(t, "-3/2"), rat(t, "7/4"))
	b := New(rat(t, "2/5"), rat(t, "9/2"))
	samples := []*big.Rat{
		rat(t, "-3/2"), rat(t, "-1"), rat(t, "0"), rat(t, "1/3"),
		rat(t, "7/4"), rat(t, "2/5"), rat(t, "1"), rat(t, "9/2"),
	}
	add, sub, mul := a.Add(b), a.Sub(b), a.Mul(b)
	div, err := a.Div(b)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, x := range samples {
		if !a.Contains(x) {
			continue
		}
		for _, y := range samples {
			if !b.Contains(y) {
				continue
			}
			if !add.Contains(new(big.Rat).Add(x, y)) {
				t.Fatalf("add enclosure violated for %s + %s", x, y)
			}
			if !sub.Contains(new(big.Rat).Sub(x, y)) {
				t.Fatalf("sub enclosure violated for %s - %s", x, y)
			}
			if !mul.Contains(new(big.Rat).Mul(x, y)) {
				t.Fatalf("mul enclosure violated for %s * %s", x, y)
			}
			if !div.Contains(new(big.Rat).Quo(x, y)) {
				t.Fatalf("div enclosure violated for %s / %s", x, y)
			}
		}
	}
}
