package netsec

import "testing"

func TestCanonicalOrigin(t *testing.T) {
	t.Parallel()

	valid := []struct {
		raw  string
		want string
	}{
		{"https://example.com", "https://example.com"},
		{"HTTPS://Example.COM", "https://example.com"},
		{"https://example.com:443", "https://example.com"},
		{"http://example.com:80", "http://example.com"},
		{"https://example.com:8443", "https://example.com:8443"},
		{"https://[2001:db8::1]:443", "https://[2001:db8::1]"},
		{" https://example.com ", "https://example.com"},
	}
	for _, tc := range valid {
		got, ok := CanonicalOrigin(tc.raw)
		if !ok || got != tc.want {
			t.Errorf("CanonicalOrigin(%q) = %q, %v; want %q, true", tc.raw, got, ok, tc.want)
		}
	}

	invalid := []string{
		"",
		" ",
		"null",
		"NULL",
		"*",
		"https://example.com/",
		"https://example.com/path",
		"https://example.com?query",
		"https://example.com#fragment",
		"https://example.com#",
		"https://example.com:",
		"https://user@example.com",
		"https://example.com:0",
		"https://example.com:99999",
		"https://example.com evil",
		"https://example.com\\.evil.com",
		"file:///etc/passwd",
	}
	for _, raw := range invalid {
		if got, ok := CanonicalOrigin(raw); ok {
			t.Errorf("CanonicalOrigin(%q) = %q, true; want rejection", raw, got)
		}
	}
}

func TestOriginAllowedExactMatch(t *testing.T) {
	t.Parallel()

	allowed := []string{"https://app.example", "http://localhost:8080", "https://app.example:443"}
	tests := []struct {
		origin string
		want   bool
	}{
		{"https://app.example", true},
		{"HTTPS://APP.EXAMPLE", true},
		{"https://app.example:443", true},
		{"http://localhost:8080", true},
		{"http://localhost", false},
		{"http://localhost:8081", false},
		{"https://app.example.evil", false},
		{"https://app.example/", false},
		{"https://app.example@evil.example", false},
		{"null", false},
		{"", false},
	}
	for _, tc := range tests {
		if got := OriginAllowed(tc.origin, allowed); got != tc.want {
			t.Errorf("OriginAllowed(%q) = %v; want %v", tc.origin, got, tc.want)
		}
	}

	if OriginAllowed("https://app.example", nil) {
		t.Error("empty allowlist must deny all origins")
	}
}
