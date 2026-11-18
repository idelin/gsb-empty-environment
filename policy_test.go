package netsec

import (
	"errors"
	"strings"
	"testing"
)

const (
	testDigestA = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testDigestB = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func mustPolicySet(t *testing.T, policies ...TrustPolicy) *PolicySet {
	t.Helper()
	set, err := NewPolicySet(policies)
	if err != nil {
		t.Fatalf("NewPolicySet: %v", err)
	}
	return set
}

func policy(name string, level VerificationLevel, scopes ...string) TrustPolicy {
	return TrustPolicy{
		Name:                  name,
		RegistryScopes:        scopes,
		SignatureVerification: SignatureVerification{Level: level},
	}
}

func TestMatchScope(t *testing.T) {
	tests := []struct {
		name  string
		scope string
		image string
		want  bool
	}{
		{"exact", "example.com/prod/app", "example.com/prod/app:latest", true},
		{"exact digest", "example.com/prod/app", "example.com/prod/app@" + testDigestA, true},
		{"exact does not match child", "example.com/prod/app", "example.com/prod/app/sub:v1", false},
		{"exact does not match sibling prefix", "example.com/prod/app", "example.com/prod/application:v1", false},
		{"wildcard child", "example.com/prod/*", "example.com/prod/app:v1", true},
		{"wildcard nested child", "example.com/prod/*", "example.com/prod/team/app@" + testDigestA, true},
		{"wildcard does not match parent", "example.com/prod/*", "example.com/prod:v1", false},
		{"wildcard does not cross repository boundary", "example.com/prod/*", "example.com/prod-evil/app:v1", false},
		{"wildcard does not match longer prefix", "example.com/prod/*", "example.com/prod2/app:v1", false},
		{"registry wildcard", "example.com/*", "example.com/team/app:v1", true},
		{"registry wildcard does not match other registry", "example.com/*", "example.org/team/app:v1", false},
		{"registry is case insensitive", "Example.COM/prod/*", "example.com/prod/app:v1", true},
		{"repository is case sensitive", "example.com/prod/*", "example.com/PROD/app:v1", false},
		{"global matches any registry", "*", "registry.internal:5000/team/app:v1", true},
		{"port in registry", "localhost:5000/prod/*", "localhost:5000/prod/app:v1", true},
		{"port is not ignored", "localhost:5000/prod/*", "localhost:5001/prod/app:v1", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := MatchScope(tt.scope, tt.image)
			if err != nil {
				t.Fatalf("MatchScope(%q, %q): %v", tt.scope, tt.image, err)
			}
			if got != tt.want {
				t.Fatalf("MatchScope(%q, %q) = %v, want %v", tt.scope, tt.image, got, tt.want)
			}
		})
	}
}

func TestValidateScope(t *testing.T) {
	valid := []string{
		"*",
		"example.com/prod",
		"example.com/prod/*",
		"example.com/*",
		"localhost:5000/prod/*",
		"[::1]:5000/prod/*",
		"example.com/team/prod/*",
	}
	for _, scope := range valid {
		if err := ValidateScope(scope); err != nil {
			t.Errorf("ValidateScope(%q) = %v, want nil", scope, err)
		}
	}

	invalid := []string{
		"",
		" *",
		"* ",
		"**",
		"*/prod",
		"example.com/prod*",
		"example.com/*/app",
		"example.com/prod/*/*",
		"example.com//app",
		"/app",
		"example.com/",
		"example.com/prod/",
		"http://example.com/prod",
		"example.com/prod:tag",
		"example.com/prod@sha256:" + strings.Repeat("a", 64),
		"example.com:abc/prod",
		"example.com:99999/prod",
		"example.com/prod/-bad",
		"example.com/prod/bad-",
	}
	for _, scope := range invalid {
		err := ValidateScope(scope)
		if err == nil {
			t.Errorf("ValidateScope(%q) = nil, want error", scope)
			continue
		}
		if !errors.Is(err, ErrInvalidScope) {
			t.Errorf("ValidateScope(%q) error = %v, want ErrInvalidScope", scope, err)
		}
	}
}

func TestInvalidScopeRejectedAtConstruction(t *testing.T) {
	_, err := NewPolicySet([]TrustPolicy{
		policy("bad", VerificationLevelStrict, "example.com/prod*"),
	})
	if !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("NewPolicySet error = %v, want ErrInvalidPolicy", err)
	}
	if !errors.Is(err, ErrInvalidScope) {
		t.Fatalf("NewPolicySet error = %v, want wrapped ErrInvalidScope", err)
	}
}

func TestInvalidVerificationLevel(t *testing.T) {
	_, err := NewPolicySet([]TrustPolicy{
		policy("bad", VerificationLevel("loose"), "example.com/prod/*"),
	})
	if !errors.Is(err, ErrInvalidPolicy) || !errors.Is(err, ErrInvalidVerificationLevel) {
		t.Fatalf("NewPolicySet error = %v, want ErrInvalidPolicy and ErrInvalidVerificationLevel", err)
	}
}

func TestSpecificScopeOverridesGlobal(t *testing.T) {
	set := mustPolicySet(t,
		policy("global", VerificationLevelPermissive, "*"),
		policy("prod", VerificationLevelStrict, "example.com/prod/*"),
	)

	match, err := set.SelectMatch("example.com/prod/app:v1")
	if err != nil {
		t.Fatalf("SelectMatch: %v", err)
	}
	if match.Policy.Name != "prod" || match.Global {
		t.Fatalf("selected policy = %q global=%v, want prod non-global", match.Policy.Name, match.Global)
	}

	match, err = set.SelectMatch("other.example/app:v1")
	if err != nil {
		t.Fatalf("SelectMatch global: %v", err)
	}
	if match.Policy.Name != "global" || !match.Global {
		t.Fatalf("selected policy = %q global=%v, want global", match.Policy.Name, match.Global)
	}
}

func TestOverlappingConcreteScopesAreConfigurationError(t *testing.T) {
	set := mustPolicySet(t,
		policy("broad", VerificationLevelPermissive, "example.com/prod/*"),
		policy("exact", VerificationLevelStrict, "example.com/prod/app"),
	)

	_, err := set.Select("example.com/prod/app:v1")
	if !errors.Is(err, ErrOverlappingScopes) {
		t.Fatalf("Select error = %v, want ErrOverlappingScopes", err)
	}
	var overlap *ScopeOverlapError
	if !errors.As(err, &overlap) {
		t.Fatalf("Select error type = %T, want *ScopeOverlapError", err)
	}
	if len(overlap.Matches) != 2 {
		t.Fatalf("overlap matches = %d, want 2", len(overlap.Matches))
	}

	// A sibling is unambiguous and still selects the wildcard policy.
	match, err := set.Select("example.com/prod/other:v1")
	if err != nil {
		t.Fatalf("Select sibling: %v", err)
	}
	if match.Name != "broad" {
		t.Fatalf("selected %q, want broad", match.Name)
	}
}

func TestOverlapWithinOnePolicy(t *testing.T) {
	set := mustPolicySet(t, policy("mixed", VerificationLevelStrict,
		"example.com/prod/*",
		"example.com/prod/app",
	))
	if _, err := set.Select("example.com/prod/app:v1"); !errors.Is(err, ErrOverlappingScopes) {
		t.Fatalf("Select error = %v, want ErrOverlappingScopes", err)
	}
}

func TestDuplicateScopesOverlap(t *testing.T) {
	set := mustPolicySet(t,
		policy("one", VerificationLevelStrict, "example.com/prod/app"),
		policy("two", VerificationLevelPermissive, "example.com/prod/app"),
	)
	if _, err := set.Select("example.com/prod/app:v1"); !errors.Is(err, ErrOverlappingScopes) {
		t.Fatalf("Select error = %v, want ErrOverlappingScopes", err)
	}
}

func TestPrefixSpoofingIsNotOverlap(t *testing.T) {
	set := mustPolicySet(t,
		policy("prod", VerificationLevelStrict, "example.com/prod/*"),
		policy("evil", VerificationLevelPermissive, "example.com/prod-evil/app"),
	)
	match, err := set.Select("example.com/prod-evil/app:v1")
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if match.Name != "evil" {
		t.Fatalf("selected %q, want evil", match.Name)
	}
}

func TestNoMatchingPolicy(t *testing.T) {
	set := mustPolicySet(t, policy("prod", VerificationLevelStrict, "example.com/prod/*"))
	_, err := set.Select("other.example/app:v1")
	if !errors.Is(err, ErrNoMatchingPolicy) {
		t.Fatalf("Select error = %v, want ErrNoMatchingPolicy", err)
	}
}

func TestInvalidImageReference(t *testing.T) {
	set := mustPolicySet(t, policy("global", VerificationLevelStrict, "*"))
	invalid := []string{
		"",
		"app",
		"example.com",
		"example.com/",
		"example.com//app",
		"example.com/prod/app:",
		"example.com/prod/app@",
		"example.com/prod/app@sha256:not-hex",
		"example.com/prod/app@md5:" + strings.Repeat("a", 32),
		"http://example.com/prod/app",
		"example.com/prod/app with space",
		"example.com/prod/*",
	}
	for _, image := range invalid {
		_, err := set.Select(image)
		if !errors.Is(err, ErrInvalidReference) {
			t.Errorf("Select(%q) error = %v, want ErrInvalidReference", image, err)
		}
	}
}

func TestParsePolicySetDocument(t *testing.T) {
	document := []byte(`{
		"version": "1.0",
		"trustPolicies": [
			{
				"name": "prod",
				"registryScopes": ["example.com/prod/*"],
				"signatureVerification": {"level": "strict"},
				"trustStores": ["ca:example"],
				"trustedIdentities": ["x509.subject: CN=prod"]
			},
			{
				"name": "global",
				"registryScopes": ["*"],
				"signatureVerification": {"level": "audit"}
			}
		]
	}`)

	set, err := ParsePolicySet(document)
	if err != nil {
		t.Fatalf("ParsePolicySet: %v", err)
	}
	if set.Len() != 2 {
		t.Fatalf("Len = %d, want 2", set.Len())
	}
	match, err := set.Select("example.com/prod/app:v1")
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if match.Name != "prod" {
		t.Fatalf("selected %q, want prod", match.Name)
	}
	policies := set.Policies()
	if len(policies[0].TrustStores) != 1 || len(policies[0].TrustedIdentities) != 1 {
		t.Fatalf("policy metadata was not preserved: %+v", policies[0])
	}
}

func TestParsePolicySetBareArray(t *testing.T) {
	document := []byte(`[
		{"name":"prod","registryScopes":["example.com/prod/*"],"signatureVerification":{"level":"permissive"}}
	]`)
	set, err := ParsePolicySet(document)
	if err != nil {
		t.Fatalf("ParsePolicySet: %v", err)
	}
	if set.Len() != 1 {
		t.Fatalf("Len = %d, want 1", set.Len())
	}
}

func TestParsePolicySetRejectsUnsupportedVersion(t *testing.T) {
	_, err := ParsePolicySet([]byte(`{"version":"2.0","trustPolicies":[]}`))
	if !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("ParsePolicySet error = %v, want ErrInvalidPolicy", err)
	}
}

func TestTopLevelVerificationLevelShorthand(t *testing.T) {
	set, err := ParsePolicySet([]byte(`{
		"version": "1.0",
		"trustPolicies": [{
			"name": "prod",
			"registryScopes": ["example.com/prod/*"],
			"verificationLevel": "permissive"
		}]
	}`))
	if err != nil {
		t.Fatalf("ParsePolicySet: %v", err)
	}
	match, err := set.Select("example.com/prod/app:v1")
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if match.SignatureVerification.Level != VerificationLevelPermissive {
		t.Fatalf("level = %q, want permissive", match.SignatureVerification.Level)
	}
}

func TestConflictingVerificationLevelsRejected(t *testing.T) {
	_, err := NewPolicySet([]TrustPolicy{{
		Name:                  "prod",
		RegistryScopes:        []string{"example.com/prod/*"},
		SignatureVerification: SignatureVerification{Level: VerificationLevelStrict},
		VerificationLevel:     VerificationLevelAudit,
	}})
	if !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("NewPolicySet error = %v, want ErrInvalidPolicy", err)
	}
}

func TestDefaultVerificationLevelIsStrict(t *testing.T) {
	set := mustPolicySet(t, TrustPolicy{
		Name:           "default",
		RegistryScopes: []string{"example.com/prod/*"},
	})
	match, err := set.Select("example.com/prod/app:v1")
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if match.SignatureVerification.Level != VerificationLevelStrict {
		t.Fatalf("level = %q, want strict", match.SignatureVerification.Level)
	}
}

func TestSelectReferenceRejectsInvalidStruct(t *testing.T) {
	set := mustPolicySet(t, policy("global", VerificationLevelStrict, "*"))
	if _, err := set.SelectReference(ImageReference{}); !errors.Is(err, ErrInvalidReference) {
		t.Fatalf("SelectReference error = %v, want ErrInvalidReference", err)
	}
	if _, err := set.SelectReference(ImageReference{
		Registry:   "example.com",
		Repository: "prod/app",
		Digest:     "md5:" + strings.Repeat("a", 32),
	}); !errors.Is(err, ErrInvalidReference) || !errors.Is(err, ErrInvalidDigest) {
		t.Fatalf("SelectReference error = %v, want ErrInvalidReference and ErrInvalidDigest", err)
	}
}

func TestDuplicateGlobalScopesAreAmbiguous(t *testing.T) {
	set := mustPolicySet(t,
		policy("global-one", VerificationLevelStrict, "*"),
		policy("global-two", VerificationLevelAudit, "*"),
	)
	if _, err := set.Select("example.com/prod/app:v1"); !errors.Is(err, ErrOverlappingScopes) {
		t.Fatalf("Select error = %v, want ErrOverlappingScopes", err)
	}
}

func TestSelectedPolicyIsACopy(t *testing.T) {
	set := mustPolicySet(t, policy("prod", VerificationLevelStrict, "example.com/prod/*"))
	first, err := set.Select("example.com/prod/app:v1")
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	first.Name = "mutated"
	first.RegistryScopes[0] = "*"

	second, err := set.Select("example.com/prod/app:v1")
	if err != nil {
		t.Fatalf("Select again: %v", err)
	}
	if second.Name != "prod" || second.RegistryScopes[0] != "example.com/prod/*" {
		t.Fatalf("policy was mutated through returned copy: %+v", second)
	}
}
