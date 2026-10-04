package domain

import (
	"math/big"
	"testing"
)

func dom(t *testing.T, expr, lo, hi string) Domain {
	t.Helper()
	e, err := Parse(expr)
	if err != nil {
		t.Fatalf("Parse(%q): %v", expr, err)
	}
	l, ok1 := new(big.Rat).SetString(lo)
	h, ok2 := new(big.Rat).SetString(hi)
	if !ok1 || !ok2 {
		t.Fatalf("bad range %q %q", lo, hi)
	}
	return DomainOf(e, Interval{Lo: l, Hi: h})
}

func TestDomains(t *testing.T) {
	cases := []struct {
		expr, lo, hi, want string
		unsupported        bool
	}{
		// constants, variable, arithmetic
		{"3/4", "-1", "1", "[-1, 1]", false},
		{"2*x + 3", "-1", "1", "[-1, 1]", false},
		{"1/(x-2)", "0", "5", "[0, 2) ∪ (2, 5]", false},
		{"1/(x-x)", "0", "1", "∅", false},
		{"x/(x+1)", "-2", "2", "[-2, -1) ∪ (-1, 2]", false},

		// even denominator: negative base rejected
		{"x^(1/2)", "-2", "4", "[0, 4]", false},
		{"(x-5)^(1/2)", "0", "3", "∅", false},
		{"((x-1)*(x-3))^(1/2)", "0", "4", "[0, 1] ∪ [3, 4]", false},
		{"(1/x)^(1/2)", "-2", "2", "(0, 2]", false},

		// exponent reduction: 2/4 == 1/2, 4/2 == 2
		{"x^(2/4)", "-2", "4", "[0, 4]", false},
		{"x^(3/6)", "-2", "4", "[0, 4]", false},
		{"x^(4/2)", "-3", "3", "[-3, 3]", false},
		{"x^(0.5)", "-2", "4", "[0, 4]", false},

		// odd denominator: negative base allowed
		{"x^(1/3)", "-8", "8", "[-8, 8]", false},
		{"x^(2/3)", "-8", "8", "[-8, 8]", false},

		// zero to a negative power is undefined
		{"x^(-1)", "-1", "2", "[-1, 0) ∪ (0, 2]", false},
		{"x^(-2)", "-1", "2", "[-1, 0) ∪ (0, 2]", false},
		{"x^(-1/2)", "-1", "4", "(0, 4]", false},
		{"x^(-1/3)", "-8", "8", "[-8, 0) ∪ (0, 8]", false},
		{"0^(-1)", "0", "1", "∅", false},
		{"(x-x)^(-2)", "0", "1", "∅", false},

		// zero exponent: constant 1 (0^0 == 1 by convention)
		{"x^0", "-1", "1", "[-1, 1]", false},

		// nested powers are not merged: (x^(1/2))^2 needs x >= 0
		// from the inner power, while (x^2)^(1/2) = |x| is total.
		{"(x^(1/2))^2", "-3", "5", "[0, 5]", false},
		{"(x^2)^(1/2)", "-4", "4", "[-4, 4]", false},
		{"(x^(1/2))^(1/2)", "-4", "16", "[0, 16]", false},

		// exactly decidable constant base with even denominator
		{"(-1)^(1/2) + x", "0", "2", "∅", false},
		{"(1/4)^(1/2) + x", "0", "2", "[0, 2]", false},

		// unsupported: zeros of x^(1/2)-1 are not exactly decidable
		// by this analyzer, so it must say so instead of guessing.
		{"1/(x^(1/2)-1)", "0", "4", "[0, 4]", true},
		{"(x^(1/2)-1)^(1/2)", "0", "4", "∅", true},
	}
	for _, c := range cases {
		d := dom(t, c.expr, c.lo, c.hi)
		if got := d.String(); got != c.want {
			t.Errorf("domain of %s over [%s, %s] = %s, want %s", c.expr, c.lo, c.hi, got, c.want)
		}
		if got := len(d.Unsupported) > 0; got != c.unsupported {
			t.Errorf("domain of %s: unsupported=%v (%v), want %v", c.expr, got, d.Unsupported, c.unsupported)
		}
	}
}

// Reduced and unreduced exponents must give identical results.
func TestExponentReductionEquivalence(t *testing.T) {
	pairs := [][2]string{
		{"x^(1/2)", "x^(2/4)"},
		{"x^(1/2)", "x^(3/6)"},
		{"x^(-1/2)", "x^(-2/4)"},
		{"x^(1/3)", "x^(2/6)"},
		{"x^2", "x^(4/2)"},
		{"(x-1)^(2/3)", "(x-1)^(4/6)"},
	}
	for _, p := range pairs {
		d1 := dom(t, p[0], "-8", "8")
		d2 := dom(t, p[1], "-8", "8")
		if d1.String() != d2.String() {
			t.Errorf("%s -> %s but %s -> %s", p[0], d1, p[1], d2)
		}
	}
}

// Nested powers must not be merged into a single power: (x^(1/2))^2
// is only defined for x >= 0, whereas the merged x^1 would be total.
func TestNestedPowersNotMerged(t *testing.T) {
	d := dom(t, "(x^(1/2))^2", "-3", "5")
	if d.String() != "[0, 5]" {
		t.Errorf("(x^(1/2))^2 domain = %s, want [0, 5]", d)
	}
	merged := dom(t, "x^1", "-3", "5")
	if merged.String() == d.String() {
		t.Errorf("nested power was wrongly merged into x^1")
	}
}

func TestParseErrors(t *testing.T) {
	for _, s := range []string{"x^x", "x^(x+1)", "x^", "(x+1", "x +", "y", "1..2"} {
		if _, err := Parse(s); err == nil {
			t.Errorf("Parse(%q) succeeded, want error", s)
		}
	}
}

func TestConstantExponentFolding(t *testing.T) {
	// 2^3 as an exponent is an exactly computable rational constant.
	d := dom(t, "x^(2^3)", "-2", "2")
	if d.String() != "[-2, 2]" {
		t.Errorf("x^(2^3) domain = %s, want [-2, 2]", d)
	}
	// (1/2)^2 in the base folds exactly to 1/4.
	d = dom(t, "x - (1/2)^2", "0", "1")
	if d.String() != "[0, 1]" {
		t.Errorf("x-(1/2)^2 domain = %s, want [0, 1]", d)
	}
}
