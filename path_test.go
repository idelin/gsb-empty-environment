package netsec

import (
	"errors"
	"testing"
)

func TestDecodePercentToFixedPoint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "/public/reports/2026.txt", "/public/reports/2026.txt"},
		{"encoded slash", "/public/..%2Fprivate/db", "/public/../private/db"},
		{"lowercase escape", "/public/..%2fprivate/db", "/public/../private/db"},
		{"double encoded slash", "/public/..%252Fprivate/db", "/public/../private/db"},
		{"triple encoded dot segment", "/public/%25252e%25252e%25252fprivate/db", "/public/../private/db"},
		{"encoded dot", "/public/%2E%2E/private/db", "/public/../private/db"},
		{"literal percent", "/public/100%25.txt", "/public/100%.txt"},
		{"no escapes", "/public/file.txt", "/public/file.txt"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := DecodePercentToFixedPoint(tt.in)
			if err != nil {
				t.Fatalf("DecodePercentToFixedPoint(%q): %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("DecodePercentToFixedPoint(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestCanonicalizePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "/public/reports/2026.txt", "/public/reports/2026.txt"},
		{"encoded traversal", "/public/..%2Fprivate/db", "/private/db"},
		{"double encoded traversal", "/public/%252e%252e%252fprivate/db", "/private/db"},
		{"dot segments", "/public/./reports/../reports/2026.txt", "/public/reports/2026.txt"},
		{"duplicate slashes", "//public///reports//2026.txt", "/public/reports/2026.txt"},
		{"trailing slash", "/public/reports/", "/public/reports"},
		{"root", "/", "/"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := Canonicalize(tt.in)
			if err != nil {
				t.Fatalf("Canonicalize(%q): %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("Canonicalize(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestCanonicalizeRejectsAmbiguity(t *testing.T) {
	t.Parallel()

	for _, in := range []string{
		"/%2e%2e/%2e%2e/private/db",
		"/..%2F..%2Fprivate/db",
		"/%252e%252e/%252e%252e/private/db",
	} {
		_, err := Canonicalize(in)
		if !errors.Is(err, ErrEscapesRoot) {
			t.Fatalf("Canonicalize(%q) error = %v, want ErrEscapesRoot", in, err)
		}
	}

	for _, in := range []string{"/public/%", "/public/%2", "/public/%GG"} {
		_, err := Canonicalize(in)
		if !errors.Is(err, ErrInvalidPercentEncoding) {
			t.Fatalf("Canonicalize(%q) error = %v, want ErrInvalidPercentEncoding", in, err)
		}
	}
}
