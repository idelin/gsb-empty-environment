package netsec

import (
	"crypto/x509"
	"errors"
	"net/url"
	"testing"
)

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	uri, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", raw, err)
	}
	return uri
}

func TestMatchURIConstraintIgnoresNonHostParts(t *testing.T) {
	constraint := "example.com"
	uris := []string{
		"https://example.com/",
		"https://Example.COM/other/path",
		"https://example.com/one?x=1",
		"https://example.com/one#fragment",
		"https://user:pass@example.com/one",
		"https://other:secret@example.com/another?a=b#c",
		"https://example.com:8443/third",
		"https://sub.example.com/any/path",
	}

	for _, raw := range uris {
		matched, err := MatchURIConstraint(mustParseURL(t, raw), constraint)
		if err != nil {
			t.Fatalf("MatchURIConstraint(%q): %v", raw, err)
		}
		if !matched {
			t.Errorf("MatchURIConstraint(%q) = false, want true", raw)
		}
	}
}

func TestMatchURIConstraintRejectsOtherHosts(t *testing.T) {
	constraint := "example.com"
	uris := []string{
		"https://example.org/",
		"https://notexample.com/",
		"https://example.com.evil.test/",
		"https://sub.example.org/",
	}

	for _, raw := range uris {
		matched, err := MatchURIConstraint(mustParseURL(t, raw), constraint)
		if err != nil {
			t.Fatalf("MatchURIConstraint(%q): %v", raw, err)
		}
		if matched {
			t.Errorf("MatchURIConstraint(%q) = true, want false", raw)
		}
	}
}

func TestMatchURIConstraintDNSRules(t *testing.T) {
	tests := []struct {
		name       string
		uri        string
		constraint string
		want       bool
	}{
		{name: "exact", uri: "https://example.com/a", constraint: "example.com", want: true},
		{name: "subdomain", uri: "https://a.example.com/b", constraint: "example.com", want: true},
		{name: "case insensitive", uri: "https://EXAMPLE.com/c", constraint: "example.COM", want: true},
		{name: "leading period subdomain", uri: "https://a.example.com/d", constraint: ".example.com", want: true},
		{name: "leading period excludes apex", uri: "https://example.com/e", constraint: ".example.com", want: false},
		{name: "asterisk is a literal label", uri: "https://a.example.com/f", constraint: "*.example.com", want: false},
		{name: "absolute host trailing dot", uri: "https://example.com./h", constraint: "example.com", want: true},
		{name: "constraint trailing dot", uri: "https://a.example.com/i", constraint: "example.com.", want: true},
		{name: "constraint uri path ignored", uri: "https://example.com/free", constraint: "https://example.com/locked", want: true},
		{name: "constraint userinfo ignored", uri: "https://example.com/j", constraint: "https://admin:secret@example.com/private", want: true},
		{name: "userinfo must not spoof host", uri: "https://example.com@evil.test/path", constraint: "example.com", want: false},
		{name: "matching host after userinfo", uri: "https://evil.test@example.com/path", constraint: "example.com", want: true},
		{name: "suffix is label based", uri: "https://notexample.com/k", constraint: "example.com", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := MatchURIConstraint(mustParseURL(t, test.uri), test.constraint)
			if err != nil {
				t.Fatalf("MatchURIConstraint(%q, %q): %v", test.uri, test.constraint, err)
			}
			if got != test.want {
				t.Errorf("MatchURIConstraint(%q, %q) = %v, want %v", test.uri, test.constraint, got, test.want)
			}
		})
	}
}

func TestMatchURIConstraintRejectsInvalidNames(t *testing.T) {
	tests := []struct {
		name string
		uri  *url.URL
		want error
	}{
		{name: "empty host", uri: mustParseURL(t, "mailto:user@example.com"), want: ErrEmptyURIHost},
		{name: "ipv4", uri: mustParseURL(t, "https://192.0.2.1/path"), want: ErrIPURIHost},
		{name: "ipv6", uri: mustParseURL(t, "https://[2001:db8::1]/path"), want: ErrIPURIHost},
		{name: "invalid port", uri: &url.URL{Scheme: "https", Host: "example.com:bad", Path: "/path"}, want: ErrInvalidURIHost},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := MatchURIConstraint(test.uri, "example.com")
			if !errors.Is(err, test.want) {
				t.Fatalf("MatchURIConstraint error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestURIConstraintsPermittedAndExcluded(t *testing.T) {
	constraints, err := NewURIConstraints(
		[]string{"example.com", "trusted.example.net"},
		[]string{".blocked.example.com", "https://evil.test/ignored"},
	)
	if err != nil {
		t.Fatalf("NewURIConstraints: %v", err)
	}

	tests := []struct {
		uri     string
		want    bool
		wantErr error
	}{
		{uri: "https://example.com/ok", want: true},
		{uri: "https://a.example.com/ok", want: true},
		{uri: "https://example.com/one?x=1", want: true},
		{uri: "https://user@example.com/two", want: true},
		{uri: "https://example.com/x", want: true},
		{uri: "https://other.example/", want: false, wantErr: ErrURINotPermitted},
		{uri: "https://example.com.evil/", want: false, wantErr: ErrURINotPermitted},
		{uri: "https://a.blocked.example.com/a", want: false, wantErr: ErrExcludedURI},
		{uri: "https://evil.test/other/path", want: false, wantErr: ErrExcludedURI},
		{uri: "https://user:pass@evil.test/other?x=1#f", want: false, wantErr: ErrExcludedURI},
	}

	for _, test := range tests {
		uri := mustParseURL(t, test.uri)
		got, err := constraints.Permits(uri)
		if err != nil {
			t.Fatalf("Permits(%q): %v", test.uri, err)
		}
		if got != test.want {
			t.Errorf("Permits(%q) = %v, want %v", test.uri, got, test.want)
		}

		err = constraints.Check(uri)
		if test.wantErr == nil && err != nil {
			t.Errorf("Check(%q) = %v, want nil", test.uri, err)
		}
		if test.wantErr != nil && !errors.Is(err, test.wantErr) {
			t.Errorf("Check(%q) = %v, want %v", test.uri, err, test.wantErr)
		}
	}
}

func TestURIConstraintsEmptyPermittedAllowsAnyNonExcludedHost(t *testing.T) {
	constraints, err := NewURIConstraints(nil, []string{"evil.test"})
	if err != nil {
		t.Fatalf("NewURIConstraints: %v", err)
	}

	allowed, err := constraints.Permits(mustParseURL(t, "https://example.com/path"))
	if err != nil {
		t.Fatalf("Permits: %v", err)
	}
	if !allowed {
		t.Fatal("Permits = false, want true")
	}

	allowed, err = constraints.Permits(mustParseURL(t, "https://evil.test/other"))
	if err != nil {
		t.Fatalf("Permits: %v", err)
	}
	if allowed {
		t.Fatal("Permits = true for excluded host, want false")
	}
}

func TestCheckCertificateURIConstraints(t *testing.T) {
	cert := &x509.Certificate{
		URIs: []*url.URL{
			mustParseURL(t, "https://user@example.com/a"),
			mustParseURL(t, "https://example.com/b?q=1"),
		},
	}
	if err := CheckCertificateURIConstraints(cert, []string{"example.com"}, nil); err != nil {
		t.Fatalf("CheckCertificateURIConstraints: %v", err)
	}

	cert.URIs = append(cert.URIs, mustParseURL(t, "https://other.test/c"))
	if err := CheckCertificateURIConstraints(cert, []string{"example.com"}, nil); !errors.Is(err, ErrURINotPermitted) {
		t.Fatalf("CheckCertificateURIConstraints error = %v, want %v", err, ErrURINotPermitted)
	}
}
