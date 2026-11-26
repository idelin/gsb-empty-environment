package spiffefed_test

import (
	"errors"
	"testing"

	spiffefed "formal.local/w27-6accbac653"
)

func TestParseIDValid(t *testing.T) {
	t.Parallel()
	valid := []string{
		"spiffe://example.org",
		"spiffe://example.org/workload",
		"spiffe://trust_domain.example/ns/default/sa/api",
		"spiffe://a-b.c_d/ns/default/sa/api-v1.2~blue",
		"spiffe://example.org/a!$&'()*+,;=:@b",
	}
	for _, raw := range valid {
		raw := raw
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			id, err := spiffefed.ParseID(raw)
			if err != nil {
				t.Fatalf("ParseID(%q): %v", raw, err)
			}
			if got := id.String(); got != raw {
				t.Fatalf("String() = %q, want %q", got, raw)
			}
		})
	}
}

func TestParseIDRejectsNormalizationAndEncodingBypasses(t *testing.T) {
	t.Parallel()
	invalid := []string{
		"",
		"http://example.org/workload",
		"SPIFFE://example.org/workload",
		"spiffe:/example.org/workload",
		"spiffe://",
		"spiffe://Example.org/workload",
		"spiffe://example.org/",
		"spiffe://example.org//admin",
		"spiffe://example.org/ns/../admin",
		"spiffe://example.org/ns/./admin",
		"spiffe://example.org/ns/%2e%2e/admin",
		"spiffe://example.org/ns/%2Fadmin",
		"spiffe://example.org/ns/admin?role=root",
		"spiffe://example.org/ns/admin#fragment",
		"spiffe://user@example.org/ns/admin",
		"spiffe://example.org:443/ns/admin",
		"spiffe://example..org/ns/admin",
		"spiffe://.example.org/ns/admin",
		"spiffe://example.org./ns/admin",
		"spiffe:///ns/admin",
		"spiffe://exam ple.org/ns/admin",
		"spiffe://example.org/ns/ad min",
		"spiffe://example.org/ns/\\admin",
		"spiffe://exämple.org/ns/admin",
	}
	for _, raw := range invalid {
		raw := raw
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			if _, err := spiffefed.ParseID(raw); !errors.Is(err, spiffefed.ErrInvalidSPIFFEID) {
				t.Fatalf("ParseID(%q) error = %v, want ErrInvalidSPIFFEID", raw, err)
			}
		})
	}
}

func TestFederationPolicyRequiresExplicitServiceEntry(t *testing.T) {
	t.Parallel()
	policy := mustPolicy(t, "local.example", map[string]spiffefed.ServicePolicy{
		"core":    {FederatesWith: []string{"external.example"}},
		"private": {},
	})
	if !policy.Allows("core", "local.example") {
		t.Fatal("local identity should be allowed for a configured service")
	}
	if !policy.Allows("core", "external.example") {
		t.Fatal("explicit federated identity should be allowed")
	}
	if policy.Allows("private", "external.example") {
		t.Fatal("identity must not be allowed when federates_with is absent")
	}
	if policy.Allows("missing", "local.example") {
		t.Fatal("unknown service must fail closed")
	}
}

func TestParseFederationPolicyJSONIsStrict(t *testing.T) {
	t.Parallel()
	policy, err := spiffefed.ParseFederationPolicyJSON([]byte(`{
		"local_trust_domain":"local.example",
		"services":{"core":{"federates_with":["external.example"]}}
	}`))
	if err != nil {
		t.Fatalf("parse policy: %v", err)
	}
	if !policy.Allows("core", "external.example") {
		t.Fatal("JSON allowlist was not applied")
	}

	bad := [][]byte{
		[]byte(`{"local_trust_domain":"local.example","services":{"core":{"federates_with":["*"]}}}`),
		[]byte(`{"local_trust_domain":"local.example","services":{"core":{"federates_with":["external.example"]}},"extra":true}`),
		[]byte(`{"local_trust_domain":"Local.example","services":{"core":{}}}`),
	}
	for _, data := range bad {
		if _, err := spiffefed.ParseFederationPolicyJSON(data); err == nil {
			t.Fatalf("expected strict policy parse to reject %s", data)
		}
	}
}
