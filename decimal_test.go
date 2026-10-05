package decimal

import (
	"math/big"
	"testing"
)

func TestParseStringRoundTrip(t *testing.T) {
	cases := []string{
		"0", "1", "-1", "123", "-0.05", "1.5", "0.001", "-1234.5678",
		"1000", "0.0001220703125", "3.141592653589793238462643383279",
	}
	for _, s := range cases {
		d := mustParse(t, s)
		if got := d.String(); got != s {
			t.Errorf("Parse(%q).String() = %q", s, got)
		}
	}
	// Exponent forms normalize to plain notation.
	forms := map[string]string{
		"1e3": "1000", "1.5e3": "1500", "15e-1": "1.5", "1E-3": "0.001",
		"-2.5e2": "-250", "+7": "7", "0.0": "0", "-0.00": "0",
	}
	for in, want := range forms {
		if got := mustParse(t, in).String(); got != want {
			t.Errorf("Parse(%q).String() = %q, want %q", in, got, want)
		}
	}
}

func TestParseInvalid(t *testing.T) {
	for _, s := range []string{"", ".", "e3", "1.2.3", "1e", "1e1.5", "abc", "--1", "1ee2"} {
		if _, err := Parse(s); err == nil {
			t.Errorf("Parse(%q) succeeded, want error", s)
		}
	}
}

func TestArithDifferentScales(t *testing.T) {
	cases := []struct {
		op         string
		a, b, want string
	}{
		{"add", "1.5", "0.005", "1.505"},
		{"add", "999.99", "0.01", "1000.00"},
		{"add", "-0.1", "0.02", "-0.08"},
		{"sub", "1", "0.0001", "0.9999"},
		{"sub", "-2.5", "-2.5", "0"},
		{"mul", "1.25", "-3.5", "-4.375"},
		{"mul", "0.001", "0.000001", "0.000000001"},
		{"mul", "-2", "-0.5", "1.0"},
	}
	for _, tc := range cases {
		a, b := mustParse(t, tc.a), mustParse(t, tc.b)
		var got Decimal
		switch tc.op {
		case "add":
			got = a.Add(b)
		case "sub":
			got = a.Sub(b)
		case "mul":
			got = a.Mul(b)
		}
		if got.String() != tc.want {
			t.Errorf("%s %s %s = %s, want %s", tc.a, tc.op, tc.b, got, tc.want)
		}
		if got.Rat().Cmp(mustParse(t, tc.want).Rat()) != 0 {
			t.Errorf("%s %s %s: rat mismatch", tc.a, tc.op, tc.b)
		}
	}
}

func TestCmp(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.5", "1.50", 0}, {"-2", "2", -1}, {"0.001", "0.0009", 1},
		{"0", "-0.00", 0}, {"-1.5", "-1.4", -1}, {"1e3", "999", 1},
	}
	for _, tc := range cases {
		if got := mustParse(t, tc.a).Cmp(mustParse(t, tc.b)); got != tc.want {
			t.Errorf("Cmp(%s, %s) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

// TestImmutability verifies operations never mutate their operands and
// that accessor copies cannot corrupt a Decimal.
func TestImmutability(t *testing.T) {
	x := mustParse(t, "999.99")
	y := mustParse(t, "0.01")
	sx, sy := x.String(), y.String()
	_ = x.Add(y)
	_ = x.Sub(y)
	_ = x.Mul(y)
	_ = x.Neg()
	_ = x.Abs()
	_ = x.Round(1, HalfEven)
	_ = x.Cmp(y)
	_ = x.Rat()
	if x.String() != sx || y.String() != sy {
		t.Fatalf("operands mutated: x=%s y=%s", x, y)
	}
	m := x.Mantissa()
	m.SetInt64(-12345)
	if x.String() != sx {
		t.Fatalf("Mantissa() alias mutated x: %s", x)
	}
	// NewFromInt must copy its argument.
	src := big.NewInt(7)
	d := NewFromInt(src, 0)
	src.SetInt64(8)
	if d.String() != "7" {
		t.Fatalf("NewFromInt did not copy: %s", d)
	}
}
