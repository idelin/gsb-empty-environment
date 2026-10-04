package domain

import (
	"strings"
	"testing"
)

func analyze(t *testing.T, src, lo, hi string, loOpen, hiOpen bool) Result {
	t.Helper()
	res, err := AnalyzeString(src, "x", lo, hi, loOpen, hiOpen)
	if err != nil {
		t.Fatalf("AnalyzeString(%q): %v", src, err)
	}
	return res
}

func expectDomain(t *testing.T, src, lo, hi string, loOpen, hiOpen bool, want string) {
	t.Helper()
	res := analyze(t, src, lo, hi, loOpen, hiOpen)
	if got := res.Domain.String(); got != want {
		t.Errorf("domain of %s on [%s,%s] = %s, want %s (unsupported: %v)",
			src, lo, hi, got, want, res.Unsupported)
	}
	if len(res.Unsupported) != 0 {
		t.Errorf("domain of %s on [%s,%s]: unexpected unsupported notes: %v",
			src, lo, hi, res.Unsupported)
	}
}

func expectUnsupported(t *testing.T, src, lo, hi, wantDomain string) {
	t.Helper()
	res := analyze(t, src, lo, hi, false, false)
	if got := res.Domain.String(); got != wantDomain {
		t.Errorf("domain of %s on [%s,%s] = %s, want %s", src, lo, hi, got, wantDomain)
	}
	if len(res.Unsupported) == 0 {
		t.Errorf("domain of %s on [%s,%s]: expected unsupported notes, got none",
			src, lo, hi)
	}
}

func TestConstantsAndArithmetic(t *testing.T) {
	cases := []struct{ src, lo, hi, want string }{
		{"5", "1", "2", "[1, 2]"},
		{"x", "-3", "4", "[-3, 4]"},
		{"2*x + 3/4", "-1", "1", "[-1, 1]"},
		{"x*x - 2*x + 1", "0", "5", "[0, 5]"},
		{"1/(x-1)", "0", "3", "[0, 1) ∪ (1, 3]"},
		{"1/(2*x+3)", "-5", "0", "[-5, -3/2) ∪ (-3/2, 0]"},
		{"1/(x*x+1)", "-5", "5", "[-5, 5]"},
		{"1/x + 1/(x-2)", "-1", "3", "[-1, 0) ∪ (0, 2) ∪ (2, 3]"},
		{"x/(x+1)", "-3", "0", "[-3, -1) ∪ (-1, 0]"},
		{"1/(1/(x-1))", "0", "3", "[0, 1) ∪ (1, 3]"},
	}
	for _, c := range cases {
		expectDomain(t, c.src, c.lo, c.hi, false, false, c.want)
	}
}

func TestRationalPowers(t *testing.T) {
	cases := []struct{ src, lo, hi, want string }{
		// Even denominator: negative base rejected.
		{"x^(1/2)", "-2", "2", "[0, 2]"},
		{"x^(3/2)", "-2", "2", "[0, 2]"},
		{"x^(-1/2)", "-2", "2", "(0, 2]"},
		// Odd denominator: real branch defined for negative bases.
		{"x^(1/3)", "-2", "2", "[-2, 2]"},
		{"x^(2/3)", "-2", "2", "[-2, 2]"},
		{"x^(4/3)", "-2", "2", "[-2, 2]"},
		// Zero to a negative power is undefined.
		{"x^(-1/3)", "-2", "2", "[-2, 0) ∪ (0, 2]"},
		{"x^(-2/3)", "-2", "2", "[-2, 0) ∪ (0, 2]"},
		{"(x+1)^(-2/3)", "-3", "3", "[-3, -1) ∪ (-1, 3]"},
		{"(x-2)^(-1)", "0", "5", "[0, 2) ∪ (2, 5]"},
		// Integer powers.
		{"x^2", "-2", "2", "[-2, 2]"},
		{"x^(-2)", "-2", "2", "[-2, 0) ∪ (0, 2]"},
		{"x^0", "-2", "2", "[-2, 2]"},
		// Constant bases.
		{"(-8)^(1/3)", "0", "1", "[0, 1]"},
		{"(-8)^(2/3)", "0", "1", "[0, 1]"},
		{"(-2)^(1/2)", "0", "1", "∅"},
		{"(1/2)^(1/2)", "0", "1", "[0, 1]"},
		{"0^(-2)", "0", "1", "∅"},
		{"0^(1/2)", "0", "1", "[0, 1]"},
		// Shifted bases.
		{"(x-1)^(1/2)", "0", "5", "[1, 5]"},
		{"(3-x)^(1/2)", "0", "5", "[0, 3]"},
		{"(x-1)^(1/2) * (3-x)^(1/2)", "0", "5", "[1, 3]"},
		{"(x-1)^(1/2) + (3-x)^(1/2)", "0", "5", "[1, 3]"},
		// Mixed with division.
		{"x^(1/2)/(x-2)", "-1", "4", "[0, 2) ∪ (2, 4]"},
		{"1/(x^(1/2) - 1)", "2", "9", "[2, 9]"},
	}
	for _, c := range cases {
		expectDomain(t, c.src, c.lo, c.hi, false, false, c.want)
	}
}

// Exponents are normalized by big.Rat, so 2/4 and 1/2 must behave
// identically.
func TestExponentReductionEquivalence(t *testing.T) {
	pairs := [][2]string{
		{"x^(2/4)", "x^(1/2)"},
		{"x^(-2/4)", "x^(-1/2)"},
		{"x^(4/6)", "x^(2/3)"},
		{"x^(-4/6)", "x^(-2/3)"},
		{"x^(4/2)", "x^2"},
		{"(x-1)^(6/4)", "(x-1)^(3/2)"},
	}
	for _, pair := range pairs {
		r1 := analyze(t, pair[0], "-2", "2", false, false)
		r2 := analyze(t, pair[1], "-2", "2", false, false)
		if r1.Domain.String() != r2.Domain.String() {
			t.Errorf("%s and %s differ: %s vs %s",
				pair[0], pair[1], r1.Domain, r2.Domain)
		}
	}
	expectDomain(t, "x^(2/4)", "-2", "2", false, false, "[0, 2]")
	expectDomain(t, "x^(4/2)", "-2", "2", false, false, "[-2, 2]")
}

// Nested powers must not be merged into a single power: (x^(1/2))^2 is
// only defined for x >= 0, while the merged exponent 2/2 = 1 would be
// defined everywhere.
func TestNestedPowersNotMerged(t *testing.T) {
	expectDomain(t, "(x^(1/2))^2", "-2", "2", false, false, "[0, 2]")
	expectDomain(t, "x^(2/2)", "-2", "2", false, false, "[-2, 2]")
	expectDomain(t, "(x^2)^(1/2)", "-2", "2", false, false, "[-2, 2]")
	// Odd root inside, even root outside: negative inner values rejected.
	expectDomain(t, "(x^(1/3))^(1/2)", "-8", "8", false, false, "[0, 8]")
	// (x^(1/2))^(1/2) is x^(1/4)-like but still requires x >= 0.
	expectDomain(t, "(x^(1/2))^(1/2)", "-2", "2", false, false, "[0, 2]")
	// Negative outer power of an even root: strictly positive base needed.
	expectDomain(t, "(x^(1/2))^(-1)", "-2", "2", false, false, "(0, 2]")
}

func TestOpenIntervals(t *testing.T) {
	expectDomain(t, "x^(1/2)", "-1", "4", true, true, "[0, 4)")
	expectDomain(t, "1/x", "0", "2", true, false, "(0, 2]")
	expectDomain(t, "x^(-1/3)", "-2", "2", true, true, "(-2, 0) ∪ (0, 2)")
	expectDomain(t, "1/(x-1)", "1", "1", false, false, "∅")
}

func TestUnsupportedParts(t *testing.T) {
	// Zeros of x^2-2 are irrational: no exact rational boundary exists,
	// so the analyzer must report the part as unsupported instead of
	// guessing with floats.
	expectUnsupported(t, "1/(x^2-2)", "0", "3", "∅")
	// Same expression, but provably nonzero on [2,3].
	expectDomain(t, "1/(x^2-2)", "2", "3", false, false, "[2, 3]")
	// x^(1/2)-1 touches zero at x=1 inside [1,9]; the exact zero set
	// cannot be isolated, so it is reported rather than approximated.
	expectUnsupported(t, "1/(x^(1/2)-1)", "1", "9", "∅")
	// A sum with one undecidable operand is undecidable as a whole:
	// the reported domain is empty and the reason is listed.
	expectUnsupported(t, "1/(x^2-2) + 1/(x-5)", "0", "10", "∅")
}

func TestUnsupportedNoteContent(t *testing.T) {
	res := analyze(t, "1/(x^2-2)", "0", "3", false, false)
	if len(res.Unsupported) == 0 {
		t.Fatal("expected unsupported notes")
	}
	found := false
	for _, n := range res.Unsupported {
		if strings.Contains(n, "x^2 - 2") || strings.Contains(n, "x^2") {
			found = true
		}
	}
	if !found {
		t.Errorf("unsupported notes should mention the sub-expression, got %v", res.Unsupported)
	}
}
