package normalize

import (
	"errors"
	"testing"
)

func TestNormalizeWhitespaceRules(t *testing.T) {
	rules := Rules{
		TrimLineTrailingSpace:    true,
		IgnoreTrailingBlankLines: true,
	}
	a, errA := Normalize("1 2  \n3\t\n\n\n", rules)
	b, errB := Normalize("1 2\n3", rules)
	if errA != nil || errB != nil {
		t.Fatalf("unexpected errors: %v %v", errA, errB)
	}
	if !Equal(a, b, rules) {
		t.Fatalf("expected equal after normalization: %q vs %q", a.Canonical, b.Canonical)
	}
}

func TestNumericParseFailure(t *testing.T) {
	rules := Rules{NumericTokens: true}
	_, err := Normalize("1 2 x", rules)
	var perr *ParseError
	if !errors.As(err, &perr) {
		t.Fatalf("expected ParseError, got %v", err)
	}
	if perr.Token != "x" {
		t.Fatalf("wrong token: %q", perr.Token)
	}
}

func TestFloatTolerance(t *testing.T) {
	rules := Rules{NumericTokens: true, FloatTolerance: 1e-6}
	a, _ := Normalize("0.30000000000000004", rules)
	b, _ := Normalize("0.3", rules)
	if !Equal(a, b, rules) {
		t.Fatal("expected equal within tolerance")
	}
	strict := Rules{NumericTokens: true}
	if Equal(a, b, strict) {
		t.Fatal("expected unequal without tolerance")
	}
}

func TestCaseInsensitive(t *testing.T) {
	rules := Rules{CaseInsensitive: true}
	a, _ := Normalize("Yes", rules)
	b, _ := Normalize("YES", rules)
	if !Equal(a, b, rules) {
		t.Fatal("expected equal case-insensitively")
	}
}
