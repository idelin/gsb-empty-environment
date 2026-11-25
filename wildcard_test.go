package workloadid

import (
	"errors"
	"testing"
)

func TestValidatePattern(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		wantErr error
	}{
		{"exact", "api.prod.example.com", nil},
		{"left wildcard", "*.prod.example.com", nil},
		{"middle wildcard", "api.*.example.com", ErrWildcardNotLeftmost},
		{"partial label", "ap*.example.com", ErrWildcardNotLeftmost},
		{"bare star", "*", ErrMultiLabelWildcard},
		{"single anchor", "*.com", ErrMultiLabelWildcard},
		{"punycode with wildcard", "*.xn--bcher-kva.example", ErrIDNWildcard},
		{"empty", "", ErrEmptyDNSName},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePattern(tc.pattern)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("ValidatePattern(%q) = %v, want %v", tc.pattern, err, tc.wantErr)
			}
		})
	}
}

func TestDNSMatcherMatch(t *testing.T) {
	// Pattern and an accepted name have the same label count: the "*" replaces
	// exactly one left-most label.
	matcher := DNSMatcher{AllowedPatterns: []string{"*.svc.prod.example.com", "api.svc.prod.example.com"}}

	accept := []string{"api.svc.prod.example.com", "billing.svc.prod.example.com"}
	reject := []string{
		"evil.prod",                  // unrelated suffix, never granted
		"a.b.svc.prod.example.com",   // two labels where one wildcard is allowed
		"svc.prod.example.com",       // missing the wildcard label
		"attacker.other.example.com", // different anchor
	}
	for _, name := range accept {
		ok, err := matcher.Match(name)
		if err != nil || !ok {
			t.Fatalf("expected %q accepted, got ok=%v err=%v", name, ok, err)
		}
	}
	for _, name := range reject {
		ok, err := matcher.Match(name)
		if ok {
			t.Fatalf("expected %q rejected, got ok=true", name)
		}
		if err != nil && name != "a.b.svc.prod.example.com" {
			t.Fatalf("unexpected error for %q: %v", name, err)
		}
	}
}

func TestDNSMatcherBareProdDoesNotGrant(t *testing.T) {
	// The original over-granting pattern is rejected at configuration time and
	// can never equate an attacker host with a production identity.
	if err := ValidatePattern("*.prod"); !errors.Is(err, ErrMultiLabelWildcard) {
		t.Fatalf("*.prod must be rejected, got %v", err)
	}
	matcher := DNSMatcher{AllowedPatterns: []string{"*.prod.example.com"}}
	if ok, _ := matcher.Match("evil.prod"); ok {
		t.Fatal("evil.prod must never match a production wildcard")
	}
}

func TestDNSMatcherIDNRejected(t *testing.T) {
	matcher := DNSMatcher{AllowedPatterns: []string{"*.svc.prod.example.com"}}
	// A punycode presented name must never be accepted via a wildcard.
	ok, err := matcher.Match("xn--c1yn36f.svc.prod.example.com")
	if err == nil || !errors.Is(err, ErrIDNWildcard) {
		t.Fatalf("expected ErrIDNWildcard, got ok=%v err=%v", ok, err)
	}
}

func TestDNSMatcherExactPunycode(t *testing.T) {
	matcher := DNSMatcher{AllowedPatterns: []string{"xn--c1yn36f.svc.prod.example.com"}}
	ok, err := matcher.Match("xn--c1yn36f.svc.prod.example.com")
	if err != nil || !ok {
		t.Fatalf("exact punycode match expected, got ok=%v err=%v", ok, err)
	}
}

func TestParseSPIFFE(t *testing.T) {
	cases := []struct {
		raw         string
		trustDomain string
		segments    []string
		wantErr     bool
	}{
		{"spiffe://prod.example.com/ns/payments/svc/api", "prod.example.com", []string{"ns", "payments", "svc", "api"}, false},
		{"spiffe://prod.example.com/", "prod.example.com", nil, false},
		{"spiffe://prod.example.com", "prod.example.com", nil, false},
		{"https://prod.example.com/x", "", nil, true},
		{"spiffe:///x", "", nil, true},
		{"spiffe://prod.example.com/x?y=1", "", nil, true},
		{"spiffe://prod.example.com/x#frag", "", nil, true},
		{"spiffe://10.0.0.1/path", "", nil, true},
		{"spiffe://prod.example.com/a//b", "", nil, true},
		{"spiffe://PROD.Example.COM/x", "prod.example.com", []string{"x"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			td, segs, err := ParseSPIFFE(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tc.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if td != tc.trustDomain {
				t.Fatalf("trust domain = %q, want %q", td, tc.trustDomain)
			}
			if len(segs) != len(tc.segments) {
				t.Fatalf("segments = %v, want %v", segs, tc.segments)
			}
			for i := range segs {
				if segs[i] != tc.segments[i] {
					t.Fatalf("segment %d = %q, want %q", i, segs[i], tc.segments[i])
				}
			}
		})
	}
}
