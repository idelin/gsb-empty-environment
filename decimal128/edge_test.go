package decimal128

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// TestOverflow verifies that every path detects values beyond 10^38 - 1.
func TestOverflow(t *testing.T) {
	overflowLiterals := []string{
		"100000000000000000000000000000000000000", // 10^38, 39 integer digits
		"-100000000000000000000000000000000000000",
	}
	for _, s := range overflowLiterals {
		if _, err := Parse(s, HalfAwayFromZero); !errors.Is(err, ErrOverflow) {
			t.Errorf("Parse(%q) err = %v, want ErrOverflow", s, err)
		}
	}

	// 39-digit integers cannot round into 38 digits when scale is 0.
	roundUp := "999999999999999999999999999999999999995"
	if _, err := Parse(roundUp, HalfAwayFromZero); !errors.Is(err, ErrOverflow) {
		t.Errorf("Parse(%q) err = %v, want ErrOverflow", roundUp, err)
	}
	// A 39-digit fraction whose half-up rounding reaches exactly 10^38:
	// coefficient is 38 nines followed by 5 (39 digits), scale 39, so dropping
	// the last digit rounds 999..995 up to 10^38.
	fractionHalf := "0." + strings.Repeat("9", 38) + "5"
	if got := len(strings.TrimPrefix(fractionHalf, "0.")); got != 39 {
		t.Fatalf("fixture length %d, want 39", got)
	}
	if _, err := Parse(fractionHalf, HalfAwayFromZero); !errors.Is(err, ErrOverflow) {
		t.Errorf("Parse(%q) err = %v, want ErrOverflow after rounding up to 10^38",
			fractionHalf, err)
	}
	// Half-even: the kept part (38 nines) is odd, so it rounds up identically.
	if _, err := Parse(fractionHalf, HalfToEven); !errors.Is(err, ErrOverflow) {
		t.Errorf("Parse(%q) half-even err = %v, want ErrOverflow (odd kept part)",
			fractionHalf, err)
	}
	// An even-kept half point rounds down instead: 37 nines + "85".
	evenFixture := "0." + strings.Repeat("9", 37) + "85"
	d, err := Parse(evenFixture, HalfToEven)
	if err != nil || d.String() != "0."+strings.Repeat("9", 37)+"8" {
		t.Errorf("Parse(%q) half-even = %v %s, want banker's round down",
			evenFixture, err, d.String())
	}

	max38 := mustParse(t, "99999999999999999999999999999999999999", HalfAwayFromZero)
	two := mustParse(t, "2", HalfAwayFromZero)
	if _, err := Mul(max38, two); !errors.Is(err, ErrOverflow) {
		t.Errorf("max*2 err = %v, want ErrOverflow", err)
	}
	one := mustParse(t, "1", HalfAwayFromZero)
	if _, err := Add(max38, one); !errors.Is(err, ErrOverflow) {
		t.Errorf("max+1 err = %v, want ErrOverflow", err)
	}
	// 38-digit value whose division by 0.5 needs 39 integer digits.
	bigDiv := mustParse(t, "99999999999999999999999999999999999999", HalfAwayFromZero)
	half := mustParse(t, "0.5", HalfAwayFromZero)
	if _, err := Div(bigDiv, half); !errors.Is(err, ErrOverflow) {
		t.Errorf("overflowing division err = %v, want ErrOverflow", err)
	}

	// New must reject oversized coefficients.
	if _, err := New(0, 39); !errors.Is(err, ErrInvalidScale) {
		t.Errorf("New scale 39 err = %v, want ErrInvalidScale", err)
	}
}

// TestMaxRangeMultiplyDivide keeps 38-digit operands inside range for valid
// operations, checking against the exact rational oracle.
func TestMaxRangeMultiplyDivide(t *testing.T) {
	maxPos := "99999999999999999999999999999999999999"
	maxNeg := "-" + maxPos
	a := mustParse(t, maxPos, HalfAwayFromZero)
	n := mustParse(t, maxNeg, HalfAwayFromZero)
	hundred := mustParse(t, "100", HalfAwayFromZero)
	hundredth := mustParse(t, "0.01", HalfAwayFromZero)

	p, err := MulMode(a, hundredth, HalfAwayFromZero)
	if err != nil {
		t.Fatalf("max * 0.01: %v", err)
	}
	want := mustParse(t, "999999999999999999999999999999999999.99", HalfAwayFromZero)
	if eq, _ := Cmp(p, want); eq != 0 {
		t.Errorf("max * 0.01 = %s, want %s", p.String(), want.String())
	}

	p2, err := MulMode(n, hundredth, HalfToEven)
	if err != nil {
		t.Fatalf("min * 0.01: %v", err)
	}
	if p2.Sign() != -1 {
		t.Errorf("min * 0.01 lost sign: %s", p2.String())
	}

	q, err := DivMode(a, hundred, HalfAwayFromZero)
	if err != nil {
		t.Fatalf("max / 100: %v", err)
	}
	// target scale = s1 + p2 + 1 = 3, so the exact quotient keeps 3 digits.
	wantQ := mustParse(t, "999999999999999999999999999999999999.990", HalfAwayFromZero)
	if eq, _ := Cmp(q, wantQ); eq != 0 {
		t.Errorf("max / 100 = %s, want %s", q.String(), wantQ.String())
	}

	// Sign combinations for the extremes.
	neg, err := MulMode(a, mustParse(t, "-1", HalfAwayFromZero), HalfAwayFromZero)
	if err != nil {
		t.Fatalf("max * -1: %v", err)
	}
	if eq, _ := Cmp(neg, n); eq != 0 {
		t.Errorf("max * -1 = %s, want %s", neg.String(), n.String())
	}
}

// TestCarryBorrow forces arithmetic across the 64-bit limb boundary.
func TestCarryBorrow(t *testing.T) {
	// 2^64 = 18446744073709551616.
	two64 := "18446744073709551616"
	a := mustParse(t, two64, HalfAwayFromZero)
	one := mustParse(t, "1", HalfAwayFromZero)

	plus, err := Add(a, one)
	if err != nil {
		t.Fatalf("2^64 + 1: %v", err)
	}
	if plus.String() != "18446744073709551617" {
		t.Errorf("2^64 + 1 = %s", plus.String())
	}

	minus, err := Sub(a, one)
	if err != nil {
		t.Fatalf("2^64 - 1: %v", err)
	}
	if minus.String() != "18446744073709551615" {
		t.Errorf("2^64 - 1 = %s", minus.String())
	}

	// Negative crossing: 1 - 2^64.
	cross, err := Sub(one, a)
	if err != nil {
		t.Fatalf("1 - 2^64: %v", err)
	}
	if cross.String() != "-18446744073709551615" {
		t.Errorf("1 - 2^64 = %s", cross.String())
	}

	// Multiplication spanning limbs: 2^64 * 2^64 = 2^128, a 39-digit number
	// that must be rejected rather than silently wrapped.
	if _, err := MulMode(a, a, HalfAwayFromZero); !errors.Is(err, ErrOverflow) {
		t.Errorf("2^64 squared err = %v, want ErrOverflow", err)
	}
	// A limb-spanning product that stays inside 38 digits: 2^64 * 1000.
	thousand := mustParse(t, "1000", HalfAwayFromZero)
	prod, err := MulMode(a, thousand, HalfAwayFromZero)
	if err != nil {
		t.Fatalf("2^64 * 1000: %v", err)
	}
	wantProd := mustParse(t, "18446744073709551616000", HalfAwayFromZero)
	if eq, _ := Cmp(prod, wantProd); eq != 0 {
		t.Errorf("2^64 * 1000 = %s, want %s", prod.String(), wantProd.String())
	}

	// Decimal alignment carry across limbs.
	da := mustParse(t, "18446744073709551615.9", HalfAwayFromZero)
	db := mustParse(t, "0.1", HalfAwayFromZero)
	s, err := Add(da, db)
	if err != nil {
		t.Fatalf("limb carry add: %v", err)
	}
	if s.String() != two64+".0" {
		t.Errorf("carry across limbs = %s, want %s.0", s.String(), two64)
	}
}

// TestResultTypes checks the SQL-style derivation helpers against known rules.
func TestResultTypes(t *testing.T) {
	p, s := ResultTypeAdd(10, 2, 8, 3)
	if p != 12 || s != 3 {
		t.Errorf("ResultTypeAdd = (%d,%d), want (12,3)", p, s)
	}
	p, s = ResultTypeMul(10, 2, 8, 3)
	if p != 19 || s != 5 {
		t.Errorf("ResultTypeMul = (%d,%d), want (19,5)", p, s)
	}
	p, s = ResultTypeDiv(10, 2, 8, 3)
	// s = max(6, 2+8+1) = 11; integer = max(8,5) = 8; p = 8+11+1 = 20.
	if p != 20 || s != 11 {
		t.Errorf("ResultTypeDiv = (%d,%d), want (20,11)", p, s)
	}
	// Capping at precision 38.
	p, s = ResultTypeAdd(38, 10, 38, 10)
	// integer = 28, uncapped p = 28+10+1 = 39 -> cap 38, s = 38-28-1 = 9.
	if p != 38 || s != 9 {
		t.Errorf("capped ResultTypeAdd = (%d,%d), want (38,9)", p, s)
	}
}

// TestNoMathBigInCore enforces that production code never imports math/big;
// only test files may use it as the rational oracle.
func TestNoMathBigInCore(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "\"math/big\"") {
			t.Errorf("production file %s must not import math/big", name)
		}
	}
}
