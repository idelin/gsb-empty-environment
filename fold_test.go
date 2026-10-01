package constfold

import "testing"

// foldStr is a test helper: parse src, fold, render.
func foldStr(t *testing.T, src string) string {
	t.Helper()
	out, err := FoldString(src)
	if err != nil {
		t.Fatalf("FoldString(%q) error: %v", src, err)
	}
	return out
}

func TestConstantArithmetic(t *testing.T) {
	cases := map[string]string{
		"1 + 2 * 3":      "7",
		"(1 + 2) * 3":    "9",
		"10 - 4 - 3":     "3",
		"7 / 2":          "3.5",
		"7 % 3":          "1",
		"2.5 * 4":        "10",
		"-(3 + 4)":       "-7",
		"10 / (2 - 2)":   "(10 / 0)", // denominator folds; div-by-zero kept for runtime
		"1 + 2 + 3 + 4":  "10",
		"'foo' = 'foo'":  "TRUE",
		"'foo' = 'bar'":  "FALSE",
		"'abc' < 'abd'":  "TRUE",
		"TRUE = FALSE":   "FALSE",
		"NULL = 1":       "NULL",
		"NULL AND FALSE": "FALSE",
		"NULL OR TRUE":   "TRUE",
	}
	for in, want := range cases {
		if got := foldStr(t, in); got != want {
			t.Errorf("fold(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPartialFolding(t *testing.T) {
	cases := map[string]string{
		"age > 10 + 5":          "(age > 15)",
		"price * (1 + 1) > 100": "((price * 2) > 100)",
		"1 + x":                 "(1 + x)", // non-constant, untouched
	}
	for in, want := range cases {
		if got := foldStr(t, in); got != want {
			t.Errorf("fold(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAlgebraicIdentities(t *testing.T) {
	cases := map[string]string{
		"x + 0":           "x",
		"0 + x":           "x",
		"x - 0":           "x",
		"x * 1":           "x",
		"1 * x":           "x",
		"x * 0":           "0",
		"0 * x":           "0",
		"x / 1":           "x",
		"0 / x":           "0",
		"x % 1":           "0",
		"u.score * 0":     "0",
		"u.score * 0 = 0": "TRUE",
		"id = id":         "TRUE",
	}
	for in, want := range cases {
		if got := foldStr(t, in); got != want {
			t.Errorf("fold(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBooleanElimination(t *testing.T) {
	cases := map[string]string{
		"x AND TRUE":       "x",
		"TRUE AND x":       "x",
		"x AND FALSE":      "FALSE",
		"FALSE AND x":      "FALSE",
		"x OR FALSE":       "x",
		"FALSE OR x":       "x",
		"x OR TRUE":        "TRUE",
		"TRUE OR x":        "TRUE",
		"NOT TRUE":         "FALSE",
		"NOT FALSE":        "TRUE",
		"NOT NOT x":        "x",
		"1 = 1":            "TRUE",
		"1 = 2":            "FALSE",
		"2 > 1 AND y":      "y",
		"a AND b AND TRUE": "(a AND b)",
	}
	for in, want := range cases {
		if got := foldStr(t, in); got != want {
			t.Errorf("fold(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestAcceptanceCriteria is the required end-to-end scenario:
// the complex WHERE clause collapses to a single-variable inequality.
func TestAcceptanceCriteria(t *testing.T) {
	in := "u.created_at > NOW() - INTERVAL 1 DAY AND 1 = 1 AND u.score * 0 = 0"
	want := "(u.created_at > (NOW() - INTERVAL 1 DAY))"
	if got := foldStr(t, in); got != want {
		t.Errorf("fold(%q) = %q, want %q", in, got, want)
	}
}

// TestWholeConditionFoldable: a fully constant WHERE reduces to a scalar.
func TestWholeConditionFoldable(t *testing.T) {
	if got := foldStr(t, "1 + 2 * 3 = 7 AND NOT (2 > 5)"); got != "TRUE" {
		t.Errorf("got %q, want TRUE", got)
	}
}

func TestParseErrors(t *testing.T) {
	for _, bad := range []string{"1 +", "(1", "'abc", "1 2", "INTERVAL 1"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) expected error, got nil", bad)
		}
	}
}

func TestFoldDoesNotMutateInput(t *testing.T) {
	src := "age > 10 + 5 AND 1 = 1"
	e, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	before := e.String()
	_ = Fold(e)
	if e.String() != before {
		t.Errorf("Fold mutated input: %q -> %q", before, e.String())
	}
}
