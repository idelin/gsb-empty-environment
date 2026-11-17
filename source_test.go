package netsec

import (
	"errors"
	"testing"
)

func TestSourceCanonicalization(t *testing.T) {
	tests := []struct {
		name      string
		scheme    string
		authority string
		want      string
	}{
		{name: "default https port", scheme: "HTTPS", authority: "Example.COM:443", want: "https://example.com"},
		{name: "default http port", scheme: "http", authority: "example.com:80", want: "http://example.com"},
		{name: "non-default port", scheme: "https", authority: "example.com:8443", want: "https://example.com:8443"},
		{name: "ipv6 default", scheme: "https", authority: "[2001:db8::1]:443", want: "https://[2001:db8::1]"},
		{name: "trailing dot", scheme: "https", authority: "example.com.", want: "https://example.com"},
		{name: "leading zero port", scheme: "https", authority: "example.com:0443", want: "https://example.com"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source, err := NewSource(test.scheme, test.authority)
			if err != nil {
				t.Fatalf("NewSource: %v", err)
			}
			if got := source.String(); got != test.want {
				t.Fatalf("Source.String() = %q, want %q", got, test.want)
			}
		})
	}

	explicit, err := NewSource("https", "example.com:443")
	if err != nil {
		t.Fatal(err)
	}
	implicit, err := NewSource("https", "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if explicit != implicit {
		t.Fatalf("default-port sources differ: %v != %v", explicit, implicit)
	}
}

func TestSourceRejectsAmbiguousAuthorities(t *testing.T) {
	bad := []struct {
		scheme    string
		authority string
	}{
		{scheme: "ftp", authority: "example.com"},
		{scheme: "https", authority: ""},
		{scheme: "https", authority: "user@example.com"},
		{scheme: "https", authority: "example.com/path"},
		{scheme: "https", authority: "example.com?query"},
		{scheme: "https", authority: "example.com#fragment"},
		{scheme: "https", authority: "example.com\\evil"},
		{scheme: "https", authority: "exa%6dple.com"},
		{scheme: "https", authority: "example.com:"},
		{scheme: "https", authority: "example.com:0"},
		{scheme: "https", authority: "example.com:99999"},
		{scheme: "https", authority: " example.com"},
	}
	for _, test := range bad {
		source, err := NewSource(test.scheme, test.authority)
		if err == nil {
			t.Errorf("NewSource(%q, %q) = %v, want error", test.scheme, test.authority, source)
			continue
		}
		if !errors.Is(err, ErrInvalidSource) {
			t.Errorf("NewSource(%q, %q) error = %v, want ErrInvalidSource", test.scheme, test.authority, err)
		}
	}
}

func TestParseSource(t *testing.T) {
	source, err := ParseSource("HTTPS://Example.COM:443/")
	if err != nil {
		t.Fatal(err)
	}
	if got := source.String(); got != "https://example.com" {
		t.Fatalf("ParseSource = %q", got)
	}
	if _, err := ParseSource("https://example.com/a/b"); err == nil {
		t.Fatal("ParseSource accepted a path")
	}
	if _, err := ParseSource("https://user@example.com"); err == nil {
		t.Fatal("ParseSource accepted user information")
	}
}
