package netsec

import (
	"errors"
	"testing"
)

func TestNormalizeServiceURL(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "scheme host and default port",
			raw:  "HTTPS://App.Example.COM:443/callback",
			want: "https://app.example.com/callback",
		},
		{
			name: "http default port",
			raw:  "http://App.Example.COM:80/callback",
			want: "http://app.example.com/callback",
		},
		{
			name: "non-default port",
			raw:  "https://app.example.com:8443/callback",
			want: "https://app.example.com:8443/callback",
		},
		{
			name: "fragment dropped and empty path",
			raw:  "https://app.example.com#callback",
			want: "https://app.example.com/",
		},
		{
			name: "query and escaped path preserved",
			raw:  "https://app.example.com/caf%c3%a9?x=1&y=2",
			want: "https://app.example.com/caf%C3%A9?x=1&y=2",
		},
		{
			name: "unreserved escape decoded",
			raw:  "https://app.example.com/%63allback",
			want: "https://app.example.com/callback",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := NormalizeServiceURL(test.raw)
			if err != nil {
				t.Fatalf("NormalizeServiceURL(%q): %v", test.raw, err)
			}
			if got != test.want {
				t.Fatalf("NormalizeServiceURL(%q) = %q, want %q", test.raw, got, test.want)
			}
		})
	}
}

func TestNormalizeServiceURLRejectsAmbiguousURLs(t *testing.T) {
	for _, raw := range []string{
		"",
		"app.example.com/callback",
		"https:///callback",
		"https://user@app.example.com/callback",
		"https://app.example.com:70000/callback",
		"https://app.example.com/%zz",
		"https://app.example.com/a\\b",
	} {
		if _, err := NormalizeServiceURL(raw); err == nil {
			t.Fatalf("NormalizeServiceURL(%q) unexpectedly succeeded", raw)
		}
	}
}

func TestServiceURLsEqual(t *testing.T) {
	if !ServiceURLsEqual("https://App.Example.com:443/cb", "https://app.example.com/cb") {
		t.Fatal("equivalent service URLs did not compare equal")
	}
	if ServiceURLsEqual("https://app.example.com/cb", "https://evil.example.com/cb") {
		t.Fatal("different service URLs compared equal")
	}
	if !errors.Is(ErrServiceMismatch, ErrServiceMismatch) {
		t.Fatal("validation error identity is broken")
	}
}
