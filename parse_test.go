package domain

import "testing"

func TestParseRoundTrip(t *testing.T) {
	cases := []struct{ src, want string }{
		{"x", "x"},
		{"3/4", "3/4"},
		{"1.5", "3/2"},
		{"x+1", "x + 1"},
		{"2*x-1", "2*x - 1"},
		{"-(x-1)", "-(x - 1)"},
		{"x^2", "x^2"},
		{"x^-1", "x^(-1)"},
		{"x^(1/2)", "x^(1/2)"},
		{"x^(-2/3)", "x^(-2/3)"},
		{"(x^(1/2))^2", "(x^(1/2))^2"},
		{"1/(x-1)", "1/(x - 1)"},
		{"x^(2/4)", "x^(1/2)"}, // exponent normalized at parse time
	}
	for _, c := range cases {
		e, err := Parse(c.src, "x")
		if err != nil {
			t.Errorf("Parse(%q): %v", c.src, err)
			continue
		}
		if got := e.String(); got != c.want {
			t.Errorf("Parse(%q).String() = %q, want %q", c.src, got, c.want)
		}
	}
}

func TestParseErrors(t *testing.T) {
	for _, src := range []string{
		"", "x +", "*x", "x^", "x^(1/0)", "x^(1/-2)", "y + 1", "x^^2", "(x",
	} {
		if _, err := Parse(src, "x"); err == nil {
			t.Errorf("Parse(%q): expected error", src)
		}
	}
}

func TestNestedPowersStayNested(t *testing.T) {
	e, err := Parse("(x^2)^(1/2)", "x")
	if err != nil {
		t.Fatal(err)
	}
	if e.Kind != KPow || e.A.Kind != KPow {
		t.Fatalf("nested powers were merged or mangled: %s", e)
	}
}
