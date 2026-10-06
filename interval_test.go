package interval

import (
	"math/big"
	"testing"
)

func rat(num, den int64) *big.Rat { return big.NewRat(num, den) }

func iv(loN, loD, hiN, hiD int64) Interval {
	return New(rat(loN, loD), rat(hiN, hiD))
}

// checkExact verifies the exact rational endpoints of got.
func checkExact(t *testing.T, name string, got Interval, lo, hi *big.Rat) {
	t.Helper()
	if got.Unbounded {
		t.Fatalf("%s: unexpected unbounded result", name)
	}
	if got.Lo.Cmp(lo) != 0 || got.Hi.Cmp(hi) != 0 {
		t.Fatalf("%s: got [%v, %v], want [%v, %v]", name, got.Lo, got.Hi, lo, hi)
	}
}

// checkDecimalContains parses the decimal output back as exact rationals
// and verifies it contains the exact interval.
func checkDecimalContains(t *testing.T, name string, iv Interval, prec int) {
	t.Helper()
	loStr, hiStr := iv.Decimal(prec)
	lo, ok := new(big.Rat).SetString(loStr)
	if !ok {
		t.Fatalf("%s: cannot parse lo %q", name, loStr)
	}
	hi, ok := new(big.Rat).SetString(hiStr)
	if !ok {
		t.Fatalf("%s: cannot parse hi %q", name, hiStr)
	}
	if lo.Cmp(iv.Lo) > 0 {
		t.Fatalf("%s: decimal lo %v > exact lo %v", name, lo, iv.Lo)
	}
	if hi.Cmp(iv.Hi) < 0 {
		t.Fatalf("%s: decimal hi %v < exact hi %v", name, hi, iv.Hi)
	}
	// Outward rounding must be tight: within one ulp of the exact bound.
	ulp := new(big.Rat).SetInt(pow10(prec))
	if new(big.Rat).Sub(iv.Lo, lo).Cmp(ulp) >= 0 {
		t.Fatalf("%s: decimal lo %v not tight vs exact %v", name, lo, iv.Lo)
	}
	if new(big.Rat).Sub(hi, iv.Hi).Cmp(ulp) >= 0 {
		t.Fatalf("%s: decimal hi %v not tight vs exact %v", name, hi, iv.Hi)
	}
}

func TestAddSub(t *testing.T) {
	a := iv(-3, 2, 1, 3) // [-3/2, 1/3]
	b := iv(2, 5, 7, 4)  // [2/5, 7/4]
	checkExact(t, "add", Add(a, b), rat(-11, 10), rat(25, 12))
	checkExact(t, "sub", Sub(a, b), rat(-13, 4), rat(-1, 15))
	checkExact(t, "neg", Neg(a), rat(-1, 3), rat(3, 2))
}

func TestMulFourEndpoints(t *testing.T) {
	// Signs mixed so all four products matter.
	a := iv(-3, 2, 1, 3) // [-1.5, 0.333...]
	b := iv(-2, 5, 7, 4) // [-0.4, 1.75]
	// products: 0.6, -2.625, -0.1333..., 0.5833...
	checkExact(t, "mul", Mul(a, b), rat(-21, 8), rat(3, 5))

	// Both negative.
	c := iv(-5, 1, -2, 1)
	d := iv(-4, 1, -1, 1)
	checkExact(t, "mul-neg-neg", Mul(c, d), rat(2, 1), rat(20, 1))

	// Zero-crossing times positive.
	e := iv(-2, 1, 3, 1)
	f := iv(1, 2, 2, 1)
	checkExact(t, "mul-cross", Mul(e, f), rat(-4, 1), rat(6, 1))
}

func TestDegenerate(t *testing.T) {
	a := FromFrac(-7, 3)
	b := FromInt64(4)
	checkExact(t, "deg-add", Add(a, b), rat(5, 3), rat(5, 3))
	checkExact(t, "deg-mul", Mul(a, b), rat(-28, 3), rat(-28, 3))
	checkExact(t, "deg-div", Div(b, a), rat(-12, 7), rat(-12, 7))
	z := FromInt64(0)
	if got := Div(b, z); !got.Unbounded {
		t.Fatal("div by degenerate zero interval must be unbounded")
	}
}

func TestDivContainsZero(t *testing.T) {
	a := FromInt64(1)
	cases := []struct {
		name string
		d    Interval
	}{
		{"interior-zero", iv(-1, 1, 2, 1)},
		{"zero-lo", iv(0, 1, 2, 1)},
		{"zero-hi", iv(-2, 1, 0, 1)},
		{"zero-degenerate", FromInt64(0)},
	}
	for _, c := range cases {
		if got := Div(a, c.d); !got.Unbounded {
			t.Fatalf("%s: divisor contains zero, want unbounded", c.name)
		}
	}
	// Divisor strictly negative: fine, and sign flips endpoints.
	checkExact(t, "div-neg", Div(a, iv(-4, 1, -2, 1)), rat(-1, 2), rat(-1, 4))
	// Divisor strictly positive.
	checkExact(t, "div-pos", Div(iv(-3, 1, 6, 1), iv(2, 1, 4, 1)), rat(-3, 2), rat(3, 1))
}

func TestUnboundedPropagates(t *testing.T) {
	u := Div(FromInt64(1), iv(-1, 1, 1, 1))
	if !u.Unbounded {
		t.Fatal("expected unbounded")
	}
	x := iv(1, 1, 2, 1)
	for name, got := range map[string]Interval{
		"add": Add(u, x), "sub": Sub(x, u),
		"mul": Mul(u, x), "div": Div(x, u),
	} {
		if !got.Unbounded {
			t.Fatalf("%s: unbounded must propagate", name)
		}
	}
	if lo, hi := u.Decimal(3); lo != "-inf" || hi != "+inf" {
		t.Fatalf("unbounded decimal: got %q, %q", lo, hi)
	}
}

func TestDecimalOutwardRounding(t *testing.T) {
	// 1/3 must round down at lo, -1/3 down (away from zero) at lo, etc.
	a := iv(-1, 3, 1, 3)
	lo, hi := a.Decimal(2)
	if lo != "-0.34" || hi != "0.34" {
		t.Fatalf("got [%s, %s], want [-0.34, 0.34]", lo, hi)
	}
	// Exact representable values must not be widened.
	b := iv(-5, 4, 7, 8) // [-1.25, 0.875]
	lo, hi = b.Decimal(3)
	if lo != "-1.250" || hi != "0.875" {
		t.Fatalf("got [%s, %s], want [-1.250, 0.875]", lo, hi)
	}
	// Negative value just below a representable point: -1.25001 -> -1.26 at 1dp? No:
	// -1.251 rounds down to -1.3 at 1 decimal.
	c := iv(-1251, 1000, -1251, 1000)
	lo, hi = c.Decimal(1)
	if lo != "-1.3" || hi != "-1.2" {
		t.Fatalf("got [%s, %s], want [-1.3, -1.2]", lo, hi)
	}
	// Integer precision 0.
	d := iv(7, 3, 7, 3)
	lo, hi = d.Decimal(0)
	if lo != "2" || hi != "3" {
		t.Fatalf("got [%s, %s], want [2, 3]", lo, hi)
	}
}

func TestChainedOperations(t *testing.T) {
	// ((a + b) * c - d) / e with exact rational cross-check.
	a := iv(-3, 2, -1, 2) // [-1.5, -0.5]
	b := iv(1, 3, 2, 3)   // [0.333, 0.666]
	c := iv(-4, 1, -2, 1) // [-4, -2]
	d := iv(1, 1, 3, 2)   // [1, 1.5]
	e := iv(2, 1, 5, 2)   // [2, 2.5]

	s1 := Add(a, b)
	checkExact(t, "chain-s1", s1, rat(-7, 6), rat(1, 6))
	s2 := Mul(s1, c)
	checkExact(t, "chain-s2", s2, rat(-2, 3), rat(14, 3))
	s3 := Sub(s2, d)
	checkExact(t, "chain-s3", s3, rat(-13, 6), rat(11, 3))
	s4 := Div(s3, e)
	checkExact(t, "chain-s4", s4, rat(-13, 12), rat(11, 6))

	// Every intermediate result must be safely enclosed by its decimal
	// rendering at several precisions.
	for _, step := range []Interval{a, b, c, d, s1, s2, s3, s4} {
		for _, prec := range []int{0, 1, 2, 5, 17} {
			checkDecimalContains(t, "chain-decimal", step, prec)
		}
	}
}

func TestDecimalContainmentFuzz(t *testing.T) {
	// Deterministic sweep over varied signs and magnitudes.
	vals := []int64{-100, -7, -3, -1, 0, 1, 2, 5, 99}
	dens := []int64{1, 3, 7, 10}
	for _, ln := range vals {
		for _, ld := range dens {
			for _, hn := range vals {
				for _, hd := range dens {
					lo := rat(ln, ld)
					hi := rat(hn, hd)
					if lo.Cmp(hi) > 0 {
						continue
					}
					i := New(lo, hi)
					for _, prec := range []int{0, 2, 9} {
						checkDecimalContains(t, "fuzz", i, prec)
					}
					// Ops stay exact: verify sum/product endpoints by model.
					sum := Add(i, i)
					checkExact(t, "fuzz-add", sum,
						new(big.Rat).Mul(lo, rat(2, 1)),
						new(big.Rat).Mul(hi, rat(2, 1)))
				}
			}
		}
	}
}
