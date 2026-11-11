package pathx

import (
	"errors"
	"testing"
)

func TestNormalize(t *testing.T) {
	cases := []struct {
		raw     string
		decoded string
		escaped string
	}{
		{"/", "/", "/"},
		{"/api/users", "/api/users", "/api/users"},
		{"/api/", "/api/", "/api/"},
		// Encoded separator stays inside its segment on both views.
		{"/a%2Fb", "/a/b", "/a%2Fb"},
		// Encoded question mark is data, never a query delimiter.
		{"/search%3Fq=go", "/search?q=go", "/search%3Fq=go"},
		// Dot segments resolve per segment; ".." clamps at the root.
		{"/a/./b/../c", "/a/c", "/a/c"},
		{"/../a/../../b", "/b", "/b"},
		{"/a//b///", "/a/b/", "/a/b/"},
		// A lone "%" decodes to itself and is re-escaped.
		{"/x%25y", "/x%y", "/x%25y"},
		// Invalid UTF-8 survives the round trip byte for byte.
		{"/files/%C3%28", "/files/\xc3(", "/files/%C3%28"},
	}
	for _, tc := range cases {
		decoded, escaped, err := Normalize(tc.raw)
		if err != nil {
			t.Errorf("Normalize(%q): unexpected error: %v", tc.raw, err)
			continue
		}
		if decoded != tc.decoded || escaped != tc.escaped {
			t.Errorf("Normalize(%q) = (%q, %q), want (%q, %q)",
				tc.raw, decoded, escaped, tc.decoded, tc.escaped)
		}
	}
}

func TestNormalizeRejects(t *testing.T) {
	cases := []struct {
		raw string
		err error
	}{
		{"no-leading-slash", nil},
		{"/bad%zz", ErrInvalidEscape},
		{"/truncated%2", ErrInvalidEscape},
		// Control characters that only a second decode would reveal.
		{"/a%252Fb", ErrDoubleEncodedControl}, // %2F -> '/'
		{"/a%255Cb", ErrDoubleEncodedControl}, // %5C -> '\'
		{"/a%253Fb", ErrDoubleEncodedControl}, // %3F -> '?'
		{"/a%2523b", ErrDoubleEncodedControl}, // %23 -> '#'
		{"/a%2500b", ErrDoubleEncodedControl}, // %00 -> NUL
		{"/a%257Fb", ErrDoubleEncodedControl}, // %7F -> DEL
	}
	for _, tc := range cases {
		_, _, err := Normalize(tc.raw)
		if err == nil {
			t.Errorf("Normalize(%q): expected error, got nil", tc.raw)
			continue
		}
		if tc.err != nil && !errors.Is(err, tc.err) {
			t.Errorf("Normalize(%q): error = %v, want %v", tc.raw, err, tc.err)
		}
	}
}

func TestDecodeOnceAllowsResidualHarmlessEscapes(t *testing.T) {
	// "%2525" decodes once to "%25"; a second decode yields a harmless
	// literal '%', so it must pass.
	got, err := DecodeOnce("%2525")
	if err != nil || got != "%25" {
		t.Fatalf("DecodeOnce(%%2525) = %q, %v; want %%25, nil", got, err)
	}
}
