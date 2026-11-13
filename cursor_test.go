package netsec

import (
	"errors"
	"math"
	"testing"
)

func TestParseCursorAcceptsCanonicalDecimal(t *testing.T) {
	cases := map[string]uint64{
		"0":                    0,
		"1":                    1,
		"42":                   42,
		"100":                  100,
		"999999999999999999":   999999999999999999,
		"18446744073709551615": math.MaxUint64,
	}
	for in, want := range cases {
		got, err := ParseCursor(in)
		if err != nil {
			t.Fatalf("ParseCursor(%q) unexpected error: %v", in, err)
		}
		if got != want {
			t.Fatalf("ParseCursor(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestParseCursorRejectsMalformed(t *testing.T) {
	bad := []string{
		"",
		"-1",
		"+1",
		"+0",
		"-0",
		"00",
		"01",
		"007",
		"0x1f",
		"0X1F",
		"ff",
		"1e3",
		"1_000",
		" 1",
		"1 ",
		"1.0",
		"\n1",
		"18446744073709551616", // MaxUint64 + 1
		"99999999999999999999999999",
		"０", // unicode fullwidth digit
	}
	for _, in := range bad {
		if _, err := ParseCursor(in); !errors.Is(err, ErrMalformedCursor) {
			t.Fatalf("ParseCursor(%q) error = %v, want ErrMalformedCursor", in, err)
		}
	}
}
