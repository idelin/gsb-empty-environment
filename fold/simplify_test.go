package fold

import "testing"

func simplifySQL(t *testing.T, src string) string {
	t.Helper()
	e, err := Parse(src)
	if err != nil {
		t.Fatalf("parse %q: %v", src, err)
	}
	return String(Simplify(e))
}

func TestConstantFolding(t *testing.T) {
	cases := []struct{ in, want string }{
		// Pure constant subtrees fold to scalars.
		{"1 + 2 * 3", "7"},
		{"(1 + 2) * 3", "9"},
		{"10 / 4", "2"},
		{"10.0 / 4", "2.5"},
		{"7 % 3", "1"},
		{"2 * 3 + 4 * 5", "26"},
		{"-5 + 1", "-4"},
		{"'foo' = 'foo'", "TRUE"},
		{"2.5 > 2", "TRUE"},
		// Partial folding: only the constant part collapses.
		{"age > 10 + 5", "age > 15"},
		{"price * (2 + 3) <= 100", "price * 5 <= 100"},
	}
	for _, c := range cases {
		if got := simplifySQL(t, c.in); got != c.want {
			t.Errorf("simplify(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestAlgebraicIdentities(t *testing.T) {
	cases := []struct{ in, want string }{
		{"x * 1", "x"},
		{"1 * x", "x"},
		{"x + 0", "x"},
		{"0 + x", "x"},
		{"x - 0", "x"},
		{"x / 1", "x"},
		{"x * 0", "0"},
		{"0 * x", "0"},
		{"x * 1 + 0", "x"},
		{"u.score * 0 = 0", "TRUE"},
		{"NOT NOT flag", "flag"},
		{"NOT TRUE", "FALSE"},
		{"NOT FALSE", "TRUE"},
	}
	for _, c := range cases {
		if got := simplifySQL(t, c.in); got != c.want {
			t.Errorf("simplify(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestBooleanElimination(t *testing.T) {
	cases := []struct{ in, want string }{
		{"x AND TRUE", "x"},
		{"TRUE AND x", "x"},
		{"x AND FALSE", "FALSE"},
		{"x OR FALSE", "x"},
		{"FALSE OR x", "x"},
		{"x OR TRUE", "TRUE"},
		{"1 = 1 AND x", "x"},
		{"x AND 1 = 1", "x"},
		{"1 = 2 OR x", "x"},
		{"1 = 2 AND x", "FALSE"},
		{"a > 1 AND TRUE OR FALSE", "a > 1"},
	}
	for _, c := range cases {
		if got := simplifySQL(t, c.in); got != c.want {
			t.Errorf("simplify(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestAcceptance is the required boundary: the complex WHERE clause
// collapses to a single-variable inequality filter.
func TestAcceptance(t *testing.T) {
	in := "u.created_at > NOW() - INTERVAL 1 DAY AND 1 = 1 AND u.score * 0 = 0"
	want := "u.created_at > NOW() - INTERVAL 1 DAY"
	if got := simplifySQL(t, in); got != want {
		t.Errorf("simplify(%q) = %q, want %q", in, got, want)
	}
}

// TestNonDeterministicKept ensures NOW() and friends are never folded.
func TestNonDeterministicKept(t *testing.T) {
	in := "NOW() - INTERVAL (1 + 1) DAY"
	want := "NOW() - INTERVAL 2 DAY"
	if got := simplifySQL(t, in); got != want {
		t.Errorf("simplify(%q) = %q, want %q", in, got, want)
	}
}

func TestDivisionByZeroNotFolded(t *testing.T) {
	in := "1 / 0"
	if got := simplifySQL(t, in); got != in {
		t.Errorf("simplify(%q) = %q, want it left untouched", in, got)
	}
}

func TestParseRoundTrip(t *testing.T) {
	in := "u.created_at > NOW() - INTERVAL 1 DAY AND 1 = 1 AND u.score * 0 = 0"
	e, err := Parse(in)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := String(e); got != in {
		t.Errorf("round trip = %q, want %q", got, in)
	}
}
