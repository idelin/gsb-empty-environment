package intwire

import (
	"math/big"
	"strings"
	"testing"
)

func TestParseIntegerLexeme(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"0", "0"},
		{"-0", "0"},
		{"123", "123"},
		{"+456", "456"},
		{"-789", "-789"},
		{"1e3", "1000"},
		{"1.5e1", "15"},
		{"2.50e1", "25"},
		{"100e-2", "1"},
		{"-4.4e2", "-440"},
		{"0.0e5", "0"},
		{"0e-999999999", "0"},                    // zero mantissa short-circuits huge exponents
		{"9007199254740993", "9007199254740993"}, // 2^53+1 exactly
	}
	for _, c := range cases {
		v, err := ParseIntegerLexeme(c.in)
		if err != nil {
			t.Fatalf("%q: %v", c.in, err)
		}
		if v.B.String() != c.want {
			t.Fatalf("%q: got %s want %s", c.in, v.B, c.want)
		}
		if v.Raw != c.in {
			t.Fatalf("%q: raw not preserved", c.in)
		}
	}
}

func TestParseIntegerLexemeRejects(t *testing.T) {
	for _, s := range []string{
		"", "abc", "1.2.3", "e5", "1e", "--1", "1..2", "0x10", "1_000",
		"1.5", "1e-3", "3.141e2", "123e-10",
		"1e999999999",                     // over digit quota, rejected before big math
		"1e200001",                        // just over quota
		"9" + strings.Repeat("0", 200000), // 200001 digits
	} {
		if _, err := ParseIntegerLexeme(s); err == nil {
			t.Fatalf("%q accepted", s)
		}
	}
}

// Cross-check exponent lexemes against math/big as the independent
// reference: 10^n computed two ways must agree.
func TestExponentCrossCheck(t *testing.T) {
	for _, n := range []int{1, 7, 63, 400, 5000} {
		v, err := ParseIntegerLexeme("1e" + strings.TrimLeft(big.NewInt(int64(n)).String(), "+"))
		if err != nil {
			t.Fatal(err)
		}
		ref := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil)
		if v.B.Cmp(ref) != 0 {
			t.Fatalf("1e%d mismatch", n)
		}
	}
}
