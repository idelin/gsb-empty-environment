package numlit

import (
	"math/big"
	"strings"
	"testing"
)

func mustParse(t *testing.T, s string) *Value {
	t.Helper()
	v, err := Parse(s, Budget{})
	if err != nil {
		t.Fatalf("Parse(%q) unexpected error: %v", s, err)
	}
	return v
}

func mustFail(t *testing.T, s string) *Error {
	t.Helper()
	_, err := Parse(s, Budget{})
	if err == nil {
		t.Fatalf("Parse(%q) expected error, got nil", s)
	}
	pe, ok := err.(*Error)
	if !ok {
		t.Fatalf("Parse(%q) error type = %T, want *Error", s, err)
	}
	return pe
}

func TestIntegers(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"0", "0"},
		{"42", "42"},
		{"+42", "42"},
		{"-42", "-42"},
		{"1_000_000", "1000000"},
		{"0x10", "16"},
		{"0X1f", "31"},
		{"0xDEAD_BEEF", "3735928559"},
		{"-0x2a", "-42"},
		{"0x0", "0"},
		{"00_7", "7"},
	}
	for _, c := range cases {
		v := mustParse(t, c.in)
		if v.Kind != Int {
			t.Errorf("Parse(%q).Kind = Rat, want Int", c.in)
		}
		if v.Int.String() != c.want {
			t.Errorf("Parse(%q) = %s, want %s", c.in, v.Int, c.want)
		}
		if v.Span != (Span{0, len(c.in)}) {
			t.Errorf("Parse(%q).Span = %+v, want {0 %d}", c.in, v.Span, len(c.in))
		}
	}
}

func TestRationals(t *testing.T) {
	cases := []struct {
		in   string
		want string // exact rational
	}{
		{"0.5", "1/2"},
		{".5", "1/2"},
		{"5.", "5"},
		{"2.50", "5/2"},
		{"0.1", "1/10"},
		{"1e2", "100"},
		{"1E+2", "100"},
		{"1.6e1", "16"},
		{"5e-1", "1/2"},
		{"-0.25", "-1/4"},
		{"1_2.3_4e1_0", "123400000000"},
		{"0.000_001", "1/1000000"},
		{"3.14159e0", "314159/100000"},
	}
	for _, c := range cases {
		v := mustParse(t, c.in)
		if v.Kind != Rat {
			t.Errorf("Parse(%q).Kind = Int, want Rat", c.in)
		}
		want, _ := new(big.Rat).SetString(c.want)
		if v.Rat.Cmp(want) != 0 {
			t.Errorf("Parse(%q) = %s, want %s", c.in, v.Rat, want)
		}
	}
}

func TestReduction(t *testing.T) {
	v := mustParse(t, "0.125")
	if v.Rat.Denom().Cmp(big.NewInt(8)) != 0 || v.Rat.Num().Cmp(big.NewInt(1)) != 0 {
		t.Errorf("0.125 = %s, want reduced 1/8", v.Rat)
	}
	v = mustParse(t, "2.500")
	if v.Rat.Denom().Cmp(big.NewInt(2)) != 0 {
		t.Errorf("2.500 = %s, want reduced 5/2", v.Rat)
	}
}

func TestNegativeZero(t *testing.T) {
	for _, in := range []string{"-0", "-0x0", "-0.0", "-0e10", "-0.0e-10", "-00_0"} {
		v := mustParse(t, in)
		if !v.IsZero() {
			t.Errorf("Parse(%q) not zero", in)
		}
		if !v.Neg {
			t.Errorf("Parse(%q).Neg = false, want true (negative zero)", in)
		}
	}
	for _, in := range []string{"0", "+0", "0.0", "0x0"} {
		v := mustParse(t, in)
		if !v.IsZero() || v.Neg {
			t.Errorf("Parse(%q): IsZero=%v Neg=%v, want true,false", in, v.IsZero(), v.Neg)
		}
	}
	// Negative zero equals positive zero numerically.
	if !mustParse(t, "-0").Equal(mustParse(t, "0")) {
		t.Error("-0 should equal 0 numerically")
	}
	if !mustParse(t, "-0.0").Equal(mustParse(t, "0x0")) {
		t.Error("-0.0 should equal 0x0 numerically")
	}
}

func TestEquivalence(t *testing.T) {
	groups := [][]string{
		{"16", "0x10", "1.6e1", "16.00", "0x1_0", "1_6", "160e-1"},
		{"0.5", ".5", "5e-1", "0.50", ".50", "0.5e0"},
		{"1000000", "1e6", "1_000_000", "0xF4240", "10.0e5"},
		{"-42", "-0x2A", "-4.2e1", "-42.0"},
		{"0", "0x0", "0.0", "0e0", "-0", "-0.0e5"},
	}
	for _, g := range groups {
		base := mustParse(t, g[0])
		for _, s := range g[1:] {
			if !base.Equal(mustParse(t, s)) {
				t.Errorf("%q and %q should be equal", g[0], s)
			}
		}
	}
	// Distinct values must not compare equal.
	if mustParse(t, "0.1").Equal(mustParse(t, "0.3")) {
		t.Error("0.1 should not equal 0.3")
	}
	// 0.1 + 0.2 == 0.3 exactly (no float detour).
	sum := new(big.Rat).Add(mustParse(t, "0.1").Rational(), mustParse(t, "0.2").Rational())
	if sum.Cmp(mustParse(t, "0.3").Rational()) != 0 {
		t.Errorf("0.1 + 0.2 = %s, want exactly 0.3", sum)
	}
}

func TestSeparatorPositions(t *testing.T) {
	bad := []string{
		"_1", "1_", "1__2", "_", "+_1", "-_1",
		"0x_1", "0x1_", "0_x1", "0x__1",
		"1_.5", "1._5", "._5", "_.5",
		"1e_5", "1e5_", "1e+_5", "1_e5",
	}
	for _, s := range bad {
		mustFail(t, s)
	}
	good := []string{"1_2", "0x1_2", "1_2.3_4", "1.2e3_4", "1_2e3_4"}
	for _, s := range good {
		mustParse(t, s)
	}
}

func TestSyntaxErrors(t *testing.T) {
	bad := []string{
		"", "+", "-", ".", "e5", "0x", "0xG", "1e", "1e+", "1.2.3",
		"12a", "0x1p2", "1 2", "0b101", "1,000", "--1", "++1",
	}
	for _, s := range bad {
		mustFail(t, s)
	}
}

func TestBudgetDigits(t *testing.T) {
	b := Budget{MaxDigits: 4, MaxExponent: 10}
	if _, err := Parse("12345", b); err == nil {
		t.Error("12345 with MaxDigits=4 should fail")
	} else if pe := err.(*Error); !pe.Budget {
		t.Errorf("error should be marked as budget violation: %v", err)
	}
	// Leading zeros and separators do not count as significant digits.
	if _, err := Parse("0_0_0_1_2_3_4", b); err != nil {
		t.Errorf("leading zeros should not count: %v", err)
	}
	if _, err := Parse("1234", b); err != nil {
		t.Errorf("1234 within budget: %v", err)
	}
	// Hex digits count too.
	if _, err := Parse("0x12345", b); err == nil {
		t.Error("0x12345 with MaxDigits=4 should fail")
	}
}

func TestBudgetExponent(t *testing.T) {
	b := Budget{MaxDigits: 100, MaxExponent: 10}
	for _, s := range []string{"1e11", "1e-11", "1e1000000000", "1.5e12", "0.00000000001"} {
		if _, err := Parse(s, b); err == nil {
			t.Errorf("Parse(%q) with MaxExponent=10 should fail", s)
		} else if pe := err.(*Error); !pe.Budget {
			t.Errorf("Parse(%q): error should be budget violation: %v", s, err)
		}
	}
	for _, s := range []string{"1e10", "1e-10", "9.99e9"} {
		if _, err := Parse(s, b); err != nil {
			t.Errorf("Parse(%q) within budget: %v", s, err)
		}
	}
}

func TestBudgetPreventsHugeAllocation(t *testing.T) {
	// 1e100000000 would need ~40 MB of big.Int digits; the budget must
	// reject it before any such allocation.
	b := Budget{MaxDigits: 100, MaxExponent: 1000}
	if _, err := Parse("1e100000000", b); err == nil {
		t.Fatal("expected budget error")
	}
	// Fraction expansion counts against the exponent budget too: the
	// effective exponent here is -1001, past MaxExponent=1000.
	if _, err := Parse("0."+strings.Repeat("0", 1000)+"1", b); err == nil {
		t.Fatal("expected exponent budget error for 1001-digit fraction")
	}
	// But a fraction just inside the budget parses exactly.
	v, err := Parse("0."+strings.Repeat("0", 99)+"1", b)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pow := new(big.Int).Exp(big.NewInt(10), big.NewInt(100), nil)
	want := new(big.Rat).SetFrac(big.NewInt(1), pow)
	if v.Rational().Cmp(want) != 0 {
		t.Errorf("got %s, want 1e-100", v.Rational())
	}
}

func TestDefaultBudget(t *testing.T) {
	// Zero Budget selects DefaultBudget; a modest literal parses fine.
	if _, err := Parse("1.5e100", Budget{}); err != nil {
		t.Fatalf("unexpected error with default budget: %v", err)
	}
	if _, err := Parse("1e5000", Budget{}); err == nil {
		t.Fatal("1e5000 should exceed the default exponent budget")
	}
}

func TestErrorPosition(t *testing.T) {
	pe := mustFail(t, "12_")
	if pe.Pos != 2 {
		t.Errorf("error position = %d, want 2", pe.Pos)
	}
	pe = mustFail(t, "0x1G")
	if pe.Pos != 3 {
		t.Errorf("error position = %d, want 3", pe.Pos)
	}
}
