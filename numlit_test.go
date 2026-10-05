package numlit

import (
	"errors"
	"math/big"
	"strings"
	"testing"
)

func mustParse(t *testing.T, s string) *Value {
	t.Helper()
	v, err := Parse(s)
	if err != nil {
		t.Fatalf("Parse(%q): %v", s, err)
	}
	return v
}

func checkInt(t *testing.T, s, want string) {
	t.Helper()
	v := mustParse(t, s)
	if v.Kind() != Int {
		t.Fatalf("Parse(%q).Kind() = %v, want Int", s, v.Kind())
	}
	n, ok := v.Int()
	if !ok {
		t.Fatalf("Parse(%q).Int() not ok", s)
	}
	w, _ := new(big.Int).SetString(want, 10)
	if n.Cmp(w) != 0 {
		t.Fatalf("Parse(%q) = %s, want %s", s, n, w)
	}
	if v.Den().Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("Parse(%q).Den() = %s, want 1", s, v.Den())
	}
}

func checkRat(t *testing.T, s, wantNum, wantDen string) {
	t.Helper()
	v := mustParse(t, s)
	if v.Kind() != Rat {
		t.Fatalf("Parse(%q).Kind() = %v, want Rat", s, v.Kind())
	}
	wn, _ := new(big.Int).SetString(wantNum, 10)
	wd, _ := new(big.Int).SetString(wantDen, 10)
	if v.Num().Cmp(wn) != 0 || v.Den().Cmp(wd) != 0 {
		t.Fatalf("Parse(%q) = %s/%s, want %s/%s", s, v.Num(), v.Den(), wn, wd)
	}
	if _, ok := v.Int(); ok {
		t.Fatalf("Parse(%q).Int() ok, want not ok", s)
	}
}

func TestIntegers(t *testing.T) {
	checkInt(t, "0", "0")
	checkInt(t, "42", "42")
	checkInt(t, "+7", "7")
	checkInt(t, "-13", "-13")
	checkInt(t, "1_000_000", "1000000")
	checkInt(t, "007", "7")
	big256 := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	v := mustParse(t, "0x"+strings.Repeat("f", 64))
	n, _ := v.Int()
	if n.Cmp(big256) != 0 {
		t.Fatalf("0xff..ff = %s, want %s", n, big256)
	}
}

func TestHexIntegers(t *testing.T) {
	checkInt(t, "0x1F", "31")
	checkInt(t, "0Xff_ff", "65535")
	checkInt(t, "0xDEAD_BEEF", "3735928559")
	checkInt(t, "-0x2a", "-42")
	checkInt(t, "0x0_0", "0")
}

func TestDecimalsAndExponents(t *testing.T) {
	checkRat(t, "0.5", "1", "2")
	checkRat(t, "1.5", "3", "2")
	checkRat(t, "-0.25", "-1", "4")
	checkRat(t, "0.015", "3", "200")
	checkRat(t, "2.50", "5", "2")
	checkRat(t, "1.5e-2", "3", "200")
	checkRat(t, "5e-1", "1", "2")
	checkRat(t, "1e-2", "1", "100")
	checkInt(t, "1e3", "1000")
	checkInt(t, "1.25e2", "125")
	checkInt(t, "1.0", "1")
	checkInt(t, "100e-2", "1")
	checkInt(t, "5.", "5")
	checkRat(t, ".5", "1", "2")
	checkInt(t, "1.e3", "1000")
	checkInt(t, "1e1_0", "10000000000")
	checkRat(t, "1_2.3_4e0", "617", "50")
}

func TestNegativeZero(t *testing.T) {
	for _, s := range []string{"-0", "-0.0", "-0e10", "-0.0e-5", "-0x0", "-0_0"} {
		v := mustParse(t, s)
		if !v.IsZero() {
			t.Errorf("Parse(%q).IsZero() = false", s)
		}
		if v.Sign() != -1 {
			t.Errorf("Parse(%q).Sign() = %d, want -1", s, v.Sign())
		}
		if v.Kind() != Int {
			t.Errorf("Parse(%q).Kind() = %v, want Int", s, v.Kind())
		}
	}
	neg := mustParse(t, "-0")
	pos := mustParse(t, "0")
	if !neg.Equal(pos) {
		t.Error("-0 must equal 0 numerically")
	}
	if neg.Sign() == pos.Sign() {
		t.Error("sign of -0 must be preserved and differ from 0")
	}
	if neg.Cmp(pos) != 0 {
		t.Error("Cmp(-0, 0) != 0")
	}
}

func TestSeparatorBoundaries(t *testing.T) {
	invalid := []string{
		"_1", "1_", "1__2", "1__",
		"0x_FF", "0x1F_", "0x__1F", "0x",
		"1_.5", "1._5", "1._", "1.5__3",
		"1e_5", "1e5_", "1_e5", "1.5_e3", "1e+_5",
		"", "+", "-", ".", "e5", "1e", "1e+", "0x1.5", "1.2.3", "12a", "0b101",
	}
	for _, s := range invalid {
		if _, err := Parse(s); !errors.Is(err, ErrSyntax) {
			t.Errorf("Parse(%q) err = %v, want ErrSyntax", s, err)
		}
	}
	valid := []string{
		"1_000", "0xDEAD_BEEF", "1_2.3_4e5_6", "0x0_0", "1_0",
		"0_1", "9_9.9_9e-1_0",
	}
	for _, s := range valid {
		if _, err := Parse(s); err != nil {
			t.Errorf("Parse(%q) err = %v, want nil", s, err)
		}
	}
}

func TestEquivalentSpellings(t *testing.T) {
	pairs := [][2]string{
		{"1_000", "1000"},
		{"0x64", "100"},
		{"0X1_0", "16"},
		{"0xA", "10"},
		{"0xA", "1e1"},
		{"0xA", "10.0"},
		{"0xA", "1_0"},
		{"0.5", "5e-1"},
		{"0.5", "0.50"},
		{"0.5", ".5"},
		{"0.5", "0.5e0"},
		{"2.5e1", "25"},
		{"25e-1", "2.5"},
		{"1.0", "1"},
		{"100e-2", "1"},
		{"0.125", "125e-3"},
		{"007", "7"},
		{"1_2.3_4e1", "123.4"},
		{"+9", "9"},
		{"-1.5", "-15e-1"},
		{"-0", "0"},
		{"-0.0", "0e0"},
	}
	for _, p := range pairs {
		a := mustParse(t, p[0])
		b := mustParse(t, p[1])
		if !a.Equal(b) {
			t.Errorf("%q (%s) != %q (%s)", p[0], a, p[1], b)
		}
	}
}

func TestOrdering(t *testing.T) {
	if mustParse(t, "-1").Cmp(mustParse(t, "0.5")) >= 0 {
		t.Error("-1 !< 0.5")
	}
	if mustParse(t, "0.5").Cmp(mustParse(t, "2")) >= 0 {
		t.Error("0.5 !< 2")
	}
	if mustParse(t, "0x10").Cmp(mustParse(t, "15.5")) <= 0 {
		t.Error("0x10 !> 15.5")
	}
}

func TestBudgetSignificantDigits(t *testing.T) {
	if _, err := Parse(strings.Repeat("9", 10_001)); !errors.Is(err, ErrBudget) {
		t.Fatalf("err = %v, want ErrBudget", err)
	}
	if _, err := Parse(strings.Repeat("9", 10_000)); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if _, err := Parse("0x" + strings.Repeat("f", 10_001)); !errors.Is(err, ErrBudget) {
		t.Fatalf("err = %v, want ErrBudget", err)
	}
	// Leading zeros are not significant.
	if _, err := Parse("0." + strings.Repeat("0", 999) + "1"); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
}

func TestBudgetExponent(t *testing.T) {
	for _, s := range []string{
		"1e100001", "1e-100001", "1e999999999999999999999",
		"1E+100001", "0.1e-100000", "1.5e100002",
	} {
		if _, err := Parse(s); !errors.Is(err, ErrBudget) {
			t.Errorf("Parse(%q) err = %v, want ErrBudget", s, err)
		}
	}
	if _, err := Parse("1e100000"); err != nil {
		t.Errorf("Parse(1e100000) err = %v, want nil", err)
	}
	// Zero mantissa never expands, regardless of exponent.
	for _, s := range []string{"0e999999999999", "0.0e-999999999999"} {
		v, err := Parse(s)
		if err != nil || !v.IsZero() {
			t.Errorf("Parse(%q) = %v, %v; want zero, nil", s, v, err)
		}
	}
}

func TestCustomBudget(t *testing.T) {
	b := Budget{MaxSignificantDigits: 4, MaxExponent: 3}
	if _, err := ParseWith("12345", b); !errors.Is(err, ErrBudget) {
		t.Errorf("err = %v, want ErrBudget", err)
	}
	if _, err := ParseWith("1e4", b); !errors.Is(err, ErrBudget) {
		t.Errorf("err = %v, want ErrBudget", err)
	}
	if _, err := ParseWith("1.5e-3", b); !errors.Is(err, ErrBudget) {
		t.Errorf("err = %v, want ErrBudget", err)
	}
	v, err := ParseWith("1e3", b)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if n, _ := v.Int(); n.Cmp(big.NewInt(1000)) != 0 {
		t.Errorf("1e3 = %s", n)
	}
}

func TestSpan(t *testing.T) {
	src := "foo(1_2.5e1)bar"
	v, err := ParsePrefix(src, 4, DefaultBudget)
	if err != nil {
		t.Fatalf("ParsePrefix: %v", err)
	}
	if v.Start != 4 || v.End != 11 {
		t.Fatalf("span = [%d,%d), want [4,11)", v.Start, v.End)
	}
	if src[v.Start:v.End] != "1_2.5e1" {
		t.Fatalf("span text = %q", src[v.Start:v.End])
	}
	if n, ok := v.Int(); !ok || n.Cmp(big.NewInt(125)) != 0 {
		t.Fatalf("value = %v", v)
	}
	// Full-string Parse rejects trailing garbage.
	if _, err := Parse("12a"); !errors.Is(err, ErrSyntax) {
		t.Fatalf("Parse(12a) err = %v, want ErrSyntax", err)
	}
	// Span of a plain Parse covers the whole input.
	v = mustParse(t, "-0x1F")
	if v.Start != 0 || v.End != 5 {
		t.Fatalf("span = [%d,%d), want [0,5)", v.Start, v.End)
	}
}

func TestRatAccessor(t *testing.T) {
	if got := mustParse(t, "-0.5").Rat().RatString(); got != "-1/2" {
		t.Fatalf("RatString = %s, want -1/2", got)
	}
	if got := mustParse(t, "0.1").Rat().RatString(); got != "1/10" {
		t.Fatalf("RatString = %s, want 1/10", got)
	}
	if got := mustParse(t, "3").String(); got != "3" {
		t.Fatalf("String = %s, want 3", got)
	}
	if got := mustParse(t, "-0.5").String(); got != "-1/2" {
		t.Fatalf("String = %s, want -1/2", got)
	}
}
