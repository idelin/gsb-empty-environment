package netsec_test

import (
	"errors"
	"testing"

	netsec "example.org/netsec/q0238"
)

func TestParseSPIFFEID(t *testing.T) {
	t.Parallel()

	id, err := netsec.ParseSPIFFEID("spiffe://PROD.Example.ORG/ns/default/sa/backend")
	if err != nil {
		t.Fatalf("valid SPIFFE ID rejected: %v", err)
	}
	if id.TrustDomain != "prod.example.org" {
		t.Fatalf("trust domain not canonicalized: %q", id.TrustDomain)
	}
	if id.Path != "/ns/default/sa/backend" {
		t.Fatalf("unexpected path: %q", id.Path)
	}
	if got := id.String(); got != "spiffe://prod.example.org/ns/default/sa/backend" {
		t.Fatalf("unexpected canonical ID: %q", got)
	}

	invalid := []string{
		"",
		"SPIFFE://prod.example.org/ns/default",
		"https://prod.example.org/ns/default",
		"spiffe://prod.example.org",
		"spiffe://prod.example.org/",
		"spiffe://prod.example.org//ns/default",
		"spiffe://prod.example.org/ns/../admin",
		"spiffe://prod.example.org/ns/./default",
		"spiffe://prod.example.org/ns%2Fdefault",
		"spiffe://prod.example.org:443/ns/default",
		"spiffe://user@prod.example.org/ns/default",
		"spiffe://prod.example.org/ns/default?x=1",
		"spiffe://prod.example.org/ns/default#frag",
		"spiffe://*/ns/default",
		"spiffe://prod..example.org/ns/default",
		"spiffe://prod_org/ns/default",
		"spiffe://10.0.0.1/ns/default",
	}
	for _, raw := range invalid {
		if _, err := netsec.ParseSPIFFEID(raw); err == nil {
			t.Errorf("invalid SPIFFE ID accepted: %q", raw)
		}
	}
}

func TestParseTrustDomain(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{
		"",
		"*",
		"*.prod",
		"prod..org",
		"-prod.org",
		"prod-.org",
		"10.0.0.1",
		"prod.org.",
		"prod_org",
		"prod.org:443",
		"prod/org",
	} {
		if _, err := netsec.ParseTrustDomain(raw); err == nil {
			t.Errorf("invalid trust domain accepted: %q", raw)
		}
	}
}

func TestWildcardValidationAndDepth(t *testing.T) {
	t.Parallel()

	valid := []string{
		"*.prod",
		"*.prod.example.org",
		"*.example.com",
	}
	for _, pattern := range valid {
		if err := netsec.ValidateWildcardPattern(pattern); err != nil {
			t.Errorf("valid wildcard %q rejected: %v", pattern, err)
		}
		if err := netsec.WildcardDNSName(pattern).Validate(); err != nil {
			t.Errorf("valid wildcard rule %q rejected: %v", pattern, err)
		}
	}

	invalid := map[string]error{
		"*":          netsec.ErrWildcardNotAllowed,
		"*.":         netsec.ErrWildcardNotAllowed,
		"*.*.prod":   netsec.ErrMultiLevelWildcard,
		"*.*.*":      netsec.ErrMultiLevelWildcard,
		"foo.*.prod": netsec.ErrWildcardNotAllowed,
		"*.prod.*":   netsec.ErrMultiLevelWildcard,
		"*foo.prod":  netsec.ErrWildcardNotAllowed,
		"foo*.prod":  netsec.ErrWildcardNotAllowed,
		"**.prod":    netsec.ErrWildcardNotAllowed,
		"*.prod.":    netsec.ErrWildcardNotAllowed,
		"*.10.0.0.1": netsec.ErrSANTypeSubstitution,
	}
	for pattern, want := range invalid {
		err := netsec.ValidateWildcardPattern(pattern)
		if !errors.Is(err, want) {
			t.Errorf("pattern %q: got %v, want %v", pattern, err, want)
		}
	}

	deep := netsec.DNSNameRule{Pattern: "*.prod", Use: netsec.SANUseHostname, Depth: 2}
	if err := deep.Validate(); !errors.Is(err, netsec.ErrMultiLevelWildcard) {
		t.Fatalf("depth 2 wildcard: got %v", err)
	}
	principal := netsec.DNSNameRule{Pattern: "*.prod", Use: netsec.SANUseIdentity, Depth: 1}
	if err := principal.Validate(); !errors.Is(err, netsec.ErrWildcardPrincipal) {
		t.Fatalf("identity-use wildcard: got %v", err)
	}
	address := netsec.DNSNameRule{Pattern: "*.prod", Use: netsec.SANUseAddress, Depth: 1}
	if err := address.Validate(); !errors.Is(err, netsec.ErrWildcardPrincipal) {
		t.Fatalf("address-use wildcard: got %v", err)
	}
	exactDepth := netsec.DNSNameRule{Pattern: "api.prod", Depth: 1}
	if err := exactDepth.Validate(); !errors.Is(err, netsec.ErrWildcardNotAllowed) {
		t.Fatalf("exact rule with wildcard depth: got %v", err)
	}
}

func TestWildcardMatchesOneLabelOnly(t *testing.T) {
	t.Parallel()

	rule := netsec.WildcardDNSName("*.prod")
	if err := rule.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"evil.prod", "api.prod", "EVIL.PROD"} {
		if !rule.Match(name) {
			t.Errorf("single-label name %q did not match", name)
		}
	}
	for _, name := range []string{"a.b.prod", "prod", "evil.prod.", "10.0.0.1", "evil.example.org"} {
		if rule.Match(name) {
			t.Errorf("name %q matched a one-label wildcard", name)
		}
	}

	exact := netsec.ExactDNSName("backend.prod.example.org")
	if !exact.Match("BACKEND.PROD.EXAMPLE.ORG") {
		t.Fatal("exact DNS match should be case-insensitive")
	}
	if exact.Match("evil.prod.example.org") {
		t.Fatal("exact DNS rule matched a different label")
	}
}
