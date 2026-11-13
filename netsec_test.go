package netsec_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	netsec "example.org/netsec/q0096"
)

const (
	testProviderID = "provider-a"
	testIssuer     = "https://issuer.example"
)

func baseConfig() netsec.ProviderConfig {
	return netsec.ProviderConfig{
		ProviderID: testProviderID,
		Issuer:     testIssuer,
		Audiences:  []string{"cloud-a", "cloud-b"},
		Template:   "{sub}",
		Languages: map[netsec.ClaimName]netsec.AllowedLanguage{
			netsec.ClaimSub: netsec.EnumLanguage("alice"),
		},
	}
}

func subClaims(value string) netsec.Claims {
	return netsec.NewClaims(map[string]any{
		"iss": testIssuer,
		"aud": "cloud-a",
		"sub": value,
	})
}

func TestPercentEncodingAndExactBytes(t *testing.T) {
	config := baseConfig()
	config.Template = "oidc:{repository}/{ref}"
	config.Languages = map[netsec.ClaimName]netsec.AllowedLanguage{
		netsec.ClaimRepository: netsec.EnumLanguage("Org/Repo%2F"),
		netsec.ClaimRef:        netsec.EnumLanguage("Feature/A é"),
	}

	policy, err := netsec.Publish(config)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	principal, err := policy.MapVerifiedClaims(netsec.NewClaims(map[string]any{
		"iss":        testIssuer,
		"aud":        "cloud-a",
		"repository": "Org/Repo%2F",
		"ref":        "Feature/A é",
	}))
	if err != nil {
		t.Fatalf("MapVerifiedClaims: %v", err)
	}

	want := "oidc:Org%2FRepo%252F/Feature%2FA%20%C3%A9"
	if got := principal.Name(); got != want {
		t.Fatalf("principal = %q, want %q", got, want)
	}
	if got := netsec.PercentEncodeClaimValue("A/a b%c~._-"); got != "A%2Fa%20b%25c~._-" {
		t.Fatalf("PercentEncodeClaimValue = %q", got)
	}
}

func TestCaseWhitespaceAndPathBytesArePreserved(t *testing.T) {
	config := baseConfig()
	config.Languages[netsec.ClaimSub] = netsec.EnumLanguage("Alice", "alice", " a/../b ")
	policy, err := netsec.Publish(config)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	for input, want := range map[string]string{
		"Alice":    "Alice",
		"alice":    "alice",
		" a/../b ": "%20a%2F..%2Fb%20",
	} {
		principal, err := policy.MapVerifiedClaims(subClaims(input))
		if err != nil {
			t.Fatalf("map %q: %v", input, err)
		}
		if principal.Name() != want {
			t.Fatalf("map %q = %q, want %q", input, principal.Name(), want)
		}
	}
}

func TestCustomClaimPattern(t *testing.T) {
	config := baseConfig()
	config.ClaimPatterns.Sub = "subject"
	policy, err := netsec.Publish(config)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	principal, err := policy.MapVerifiedClaims(netsec.NewClaims(map[string]any{
		"iss":     testIssuer,
		"aud":     "cloud-a",
		"subject": "alice",
	}))
	if err != nil {
		t.Fatalf("MapVerifiedClaims: %v", err)
	}
	if principal.Name() != "alice" {
		t.Fatalf("principal = %q", principal.Name())
	}
}

func TestOIDCValidation(t *testing.T) {
	policy, err := netsec.Publish(baseConfig())
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	tests := []struct {
		name   string
		claims map[string]any
		wantOK bool
	}{
		{
			name:   "single audience",
			claims: map[string]any{"iss": testIssuer, "aud": "cloud-a", "sub": "alice"},
			wantOK: true,
		},
		{
			name:   "issuer is exact",
			claims: map[string]any{"iss": testIssuer + "/", "aud": "cloud-a", "sub": "alice"},
		},
		{
			name:   "issuer is case sensitive",
			claims: map[string]any{"iss": "HTTPS://ISSUER.EXAMPLE", "aud": "cloud-a", "sub": "alice"},
		},
		{
			name:   "missing audience",
			claims: map[string]any{"iss": testIssuer, "sub": "alice"},
		},
		{
			name:   "audience not allowed",
			claims: map[string]any{"iss": testIssuer, "aud": "other", "sub": "alice"},
		},
		{
			name: "multiple audiences require azp",
			claims: map[string]any{
				"iss": testIssuer,
				"aud": []any{"cloud-a", "other"},
				"sub": "alice",
			},
		},
		{
			name: "multiple audiences with disallowed azp",
			claims: map[string]any{
				"iss": testIssuer,
				"aud": []any{"cloud-a", "other"},
				"azp": "other",
				"sub": "alice",
			},
		},
		{
			name: "multiple audiences with allowed azp",
			claims: map[string]any{
				"iss": testIssuer,
				"aud": []any{"other", "cloud-a"},
				"azp": "cloud-b",
				"sub": "alice",
			},
			wantOK: true,
		},
		{
			name:   "non-string azp",
			claims: map[string]any{"iss": testIssuer, "aud": "cloud-a", "azp": 1, "sub": "alice"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := policy.MapVerifiedClaims(netsec.NewClaims(test.claims))
			if test.wantOK && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !test.wantOK && !errors.Is(err, netsec.ErrClaims) {
				t.Fatalf("error = %v, want ErrClaims", err)
			}
		})
	}
}

func TestRuntimeClaimsRejectMissingEmptyAndNonString(t *testing.T) {
	policy, err := netsec.Publish(baseConfig())
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	for name, claims := range map[string]netsec.Claims{
		"missing":   netsec.NewClaims(map[string]any{"iss": testIssuer, "aud": "cloud-a"}),
		"empty":     subClaims(""),
		"notString": netsec.NewClaims(map[string]any{"iss": testIssuer, "aud": "cloud-a", "sub": 42}),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := policy.MapVerifiedClaims(claims); !errors.Is(err, netsec.ErrClaims) {
				t.Fatalf("error = %v, want ErrClaims", err)
			}
		})
	}
}

func TestClaimsSnapshotIsImmutable(t *testing.T) {
	audiences := []any{"cloud-a"}
	source := map[string]any{
		"iss": testIssuer,
		"aud": audiences,
		"sub": "alice",
		"nested": map[string]any{
			"value": "original",
		},
	}
	claims := netsec.NewClaims(source)

	source["sub"] = "mallory"
	audiences[0] = "mallory"
	source["nested"].(map[string]any)["value"] = "changed"

	if sub, _ := claims.String("sub"); sub != "alice" {
		t.Fatalf("sub changed to %q", sub)
	}
	if aud, _ := claims.Strings("aud"); !reflect.DeepEqual(aud, []string{"cloud-a"}) {
		t.Fatalf("aud changed to %#v", aud)
	}
	nested, ok := claims.Lookup("nested")
	if !ok || nested.(map[string]any)["value"] != "original" {
		t.Fatalf("nested claim changed: %#v", nested)
	}
	nested.(map[string]any)["value"] = "caller mutation"
	again, _ := claims.Lookup("nested")
	if again.(map[string]any)["value"] != "original" {
		t.Fatal("Lookup exposed mutable internal state")
	}
}

func TestCollisionAuditFindsLexicographicallySmallestPair(t *testing.T) {
	config := baseConfig()
	config.Template = "{namespace}-{serviceAccount}"
	config.Languages = map[netsec.ClaimName]netsec.AllowedLanguage{
		netsec.ClaimNamespace:      netsec.EnumLanguage("a", "a-b", "z"),
		netsec.ClaimServiceAccount: netsec.EnumLanguage("b", "b-c", "c"),
	}

	collision, err := netsec.Audit(config)
	if err != nil {
		t.Fatalf("Audit: %v", err)
	}
	if collision == nil {
		t.Fatal("expected collision")
	}
	if collision.Principal != "a-b-c" {
		t.Fatalf("principal = %q", collision.Principal)
	}
	wantFirst := netsec.ClaimTuple{
		netsec.ClaimNamespace:      "a",
		netsec.ClaimServiceAccount: "b-c",
	}
	wantSecond := netsec.ClaimTuple{
		netsec.ClaimNamespace:      "a-b",
		netsec.ClaimServiceAccount: "c",
	}
	if !reflect.DeepEqual(collision.First, wantFirst) {
		t.Fatalf("First = %#v, want %#v", collision.First, wantFirst)
	}
	if !reflect.DeepEqual(collision.Second, wantSecond) {
		t.Fatalf("Second = %#v, want %#v", collision.Second, wantSecond)
	}

	policy, err := netsec.Publish(config)
	if policy != nil {
		t.Fatal("Publish returned a policy for a colliding config")
	}
	var collisionErr *netsec.CollisionError
	if !errors.As(err, &collisionErr) || !errors.Is(err, netsec.ErrCollision) {
		t.Fatalf("error = %v, want CollisionError/ErrCollision", err)
	}
	if !reflect.DeepEqual(collisionErr.Collision.First, wantFirst) {
		t.Fatalf("published collision = %#v", collisionErr.Collision)
	}
}

func TestAdjacentPlaceholdersCanCollide(t *testing.T) {
	config := baseConfig()
	config.Template = "{namespace}{serviceAccount}"
	config.Languages = map[netsec.ClaimName]netsec.AllowedLanguage{
		netsec.ClaimNamespace:      netsec.EnumLanguage("a", "ab"),
		netsec.ClaimServiceAccount: netsec.EnumLanguage("bc", "c"),
	}
	collision, err := netsec.Audit(config)
	if err != nil {
		t.Fatalf("Audit: %v", err)
	}
	if collision == nil || collision.Principal != "abc" {
		t.Fatalf("collision = %#v", collision)
	}
	if collision.First[netsec.ClaimNamespace] != "a" ||
		collision.First[netsec.ClaimServiceAccount] != "bc" {
		t.Fatalf("First = %#v", collision.First)
	}
	if collision.Second[netsec.ClaimNamespace] != "ab" ||
		collision.Second[netsec.ClaimServiceAccount] != "c" {
		t.Fatalf("Second = %#v", collision.Second)
	}
}

func TestDifferentSeparatorsAndUnusedClaims(t *testing.T) {
	config := baseConfig()
	config.Template = "{namespace}/{serviceAccount}"
	config.Languages = map[netsec.ClaimName]netsec.AllowedLanguage{
		netsec.ClaimNamespace:      netsec.EnumLanguage("a"),
		netsec.ClaimServiceAccount: netsec.EnumLanguage("b/c%"),
	}
	policy, err := netsec.Publish(config)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	principal, err := policy.MapVerifiedClaims(netsec.NewClaims(map[string]any{
		"iss":            testIssuer,
		"aud":            "cloud-a",
		"namespace":      "a",
		"serviceAccount": "b/c%",
	}))
	if err != nil {
		t.Fatalf("MapVerifiedClaims: %v", err)
	}
	if principal.Name() != "a/b%2Fc%25" {
		t.Fatalf("principal = %q", principal.Name())
	}

	config = baseConfig()
	config.Template = "{sub}"
	config.Languages[netsec.ClaimNamespace] = netsec.EnumLanguage("ns1", "ns2")
	collision, err := netsec.Audit(config)
	if err != nil {
		t.Fatalf("Audit: %v", err)
	}
	if collision == nil {
		t.Fatal("unused multi-valued claim must be audited")
	}
}

func TestAllowedLanguageValidation(t *testing.T) {
	tests := []struct {
		name     string
		language netsec.AllowedLanguage
	}{
		{name: "empty enum", language: netsec.EnumLanguage("")},
		{name: "no values", language: netsec.AllowedLanguage{}},
		{
			name:     "empty prefix and suffix",
			language: netsec.PrefixSuffixLanguage("", ""),
		},
		{
			name: "mixed modes",
			language: netsec.AllowedLanguage{
				Values:   []string{"a"},
				Prefix:   "p-",
				Suffixes: []string{"b"},
			},
		},
		{
			name: "enum with stray prefix",
			language: netsec.AllowedLanguage{
				Values: []string{"a"},
				Prefix: "p-",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := baseConfig()
			config.Languages[netsec.ClaimSub] = test.language
			if err := config.Validate(); !errors.Is(err, netsec.ErrInvalidConfig) {
				t.Fatalf("Validate error = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestPrefixSuffixLanguage(t *testing.T) {
	config := baseConfig()
	config.Template = "{namespace}"
	config.Languages = map[netsec.ClaimName]netsec.AllowedLanguage{
		netsec.ClaimNamespace: netsec.PrefixSuffixLanguage("team-", "alpha", "beta"),
	}
	policy, err := netsec.Publish(config)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	principal, err := policy.MapVerifiedClaims(netsec.NewClaims(map[string]any{
		"iss":       testIssuer,
		"aud":       "cloud-a",
		"namespace": "team-beta",
	}))
	if err != nil {
		t.Fatalf("MapVerifiedClaims: %v", err)
	}
	if principal.Name() != "team-beta" {
		t.Fatalf("principal = %q", principal.Name())
	}
	if _, err := policy.MapVerifiedClaims(netsec.NewClaims(map[string]any{
		"iss":       testIssuer,
		"aud":       "cloud-a",
		"namespace": "team-gamma",
	})); !errors.Is(err, netsec.ErrClaims) {
		t.Fatalf("outside-language error = %v", err)
	}
}

func TestAuditBudgetRefusesUnclosedSearch(t *testing.T) {
	config := baseConfig()
	config.Languages[netsec.ClaimSub] = netsec.EnumLanguage(
		"a", "b", "c", "d", "e", "f", "g", "h", "i", "j",
	)
	config.AuditBudget = 5

	if _, err := netsec.Audit(config); !errors.Is(err, netsec.ErrAuditBudget) {
		t.Fatalf("Audit error = %v, want ErrAuditBudget", err)
	}
	policy, err := netsec.Publish(config)
	if policy != nil || !errors.Is(err, netsec.ErrAuditBudget) {
		t.Fatalf("Publish = (%v, %v), want nil policy and ErrAuditBudget", policy, err)
	}
}

func TestTemplateValidation(t *testing.T) {
	for _, template := range []string{
		"",
		"literal-only",
		"{sub",
		"{Sub}",
		"{}",
		"{sub-name}",
		"{{sub}}",
		"{ sub }",
	} {
		t.Run(template, func(t *testing.T) {
			config := baseConfig()
			config.Template = template
			if err := config.Validate(); !errors.Is(err, netsec.ErrInvalidConfig) {
				t.Fatalf("Validate error = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestPrincipalLengthLimit(t *testing.T) {
	config := baseConfig()
	config.MaxPrincipalBytes = 4
	config.Languages[netsec.ClaimSub] = netsec.EnumLanguage("alice")
	if _, err := netsec.Audit(config); !errors.Is(err, netsec.ErrPrincipalTooLong) {
		t.Fatalf("Audit error = %v, want ErrPrincipalTooLong", err)
	}
}

func TestMapTokenUsesHostVerifier(t *testing.T) {
	policy, err := netsec.Publish(baseConfig())
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	called := false
	principal, err := policy.MapToken(context.Background(), "signed-token", func(_ context.Context, token string) (netsec.Claims, error) {
		called = true
		if token != "signed-token" {
			t.Fatalf("token = %q", token)
		}
		return subClaims("alice"), nil
	})
	if err != nil {
		t.Fatalf("MapToken: %v", err)
	}
	if !called || principal.Name() != "alice" {
		t.Fatalf("called = %v, principal = %q", called, principal.Name())
	}

	if _, err := policy.MapToken(context.Background(), "token", nil); !errors.Is(err, netsec.ErrVerification) {
		t.Fatalf("nil verifier error = %v", err)
	}
	hostErr := errors.New("expired")
	if _, err := policy.MapToken(context.Background(), "token", func(context.Context, string) (netsec.Claims, error) {
		return netsec.Claims{}, hostErr
	}); !errors.Is(err, hostErr) || !errors.Is(err, netsec.ErrVerification) {
		t.Fatalf("host verifier error = %v", err)
	}
}

func TestGenerationBindingAndPermissionCallback(t *testing.T) {
	firstConfig := baseConfig()
	firstConfig.Generation = 101
	secondConfig := baseConfig()
	secondConfig.Generation = 102

	first, err := netsec.Publish(firstConfig)
	if err != nil {
		t.Fatalf("Publish first: %v", err)
	}
	second, err := netsec.Publish(secondConfig)
	if err != nil {
		t.Fatalf("Publish second: %v", err)
	}
	firstPrincipal, err := first.MapVerifiedClaims(subClaims("alice"))
	if err != nil {
		t.Fatalf("map first: %v", err)
	}
	secondPrincipal, err := second.MapVerifiedClaims(subClaims("alice"))
	if err != nil {
		t.Fatalf("map second: %v", err)
	}
	if firstPrincipal.Generation() != 101 || secondPrincipal.Generation() != 102 {
		t.Fatalf("generations = %d, %d", firstPrincipal.Generation(), secondPrincipal.Generation())
	}
	if firstPrincipal.ProviderID() != testProviderID {
		t.Fatalf("provider = %q", firstPrincipal.ProviderID())
	}

	called := false
	if allowed, err := second.Authorize(context.Background(), firstPrincipal, func(context.Context, netsec.Principal) (bool, error) {
		called = true
		return true, nil
	}); allowed || !errors.Is(err, netsec.ErrPrincipalBinding) || called {
		t.Fatalf("cross-generation Authorize = (%v, %v), called=%v", allowed, err, called)
	}

	allowed, err := second.Authorize(context.Background(), secondPrincipal, func(_ context.Context, principal netsec.Principal) (bool, error) {
		called = true
		if principal.Generation() != 102 {
			t.Fatalf("callback generation = %d", principal.Generation())
		}
		return true, nil
	})
	if err != nil || !allowed || !called {
		t.Fatalf("same-generation Authorize = (%v, %v), called=%v", allowed, err, called)
	}
}

func TestConfigurationDigestPreventsCrossPolicyReuse(t *testing.T) {
	firstConfig := baseConfig()
	firstConfig.Generation = 7
	firstConfig.Template = "{sub}"
	secondConfig := firstConfig
	secondConfig.Template = "{namespace}"
	secondConfig.Languages = map[netsec.ClaimName]netsec.AllowedLanguage{
		netsec.ClaimNamespace: netsec.EnumLanguage("alice"),
	}

	first, err := netsec.Publish(firstConfig)
	if err != nil {
		t.Fatalf("Publish first: %v", err)
	}
	second, err := netsec.Publish(secondConfig)
	if err != nil {
		t.Fatalf("Publish second: %v", err)
	}
	firstPrincipal, err := first.MapVerifiedClaims(subClaims("alice"))
	if err != nil {
		t.Fatalf("map first: %v", err)
	}
	secondPrincipal, err := second.MapVerifiedClaims(netsec.NewClaims(map[string]any{
		"iss":       testIssuer,
		"aud":       "cloud-a",
		"namespace": "alice",
	}))
	if err != nil {
		t.Fatalf("map second: %v", err)
	}
	if firstPrincipal.Name() != secondPrincipal.Name() {
		t.Fatalf("expected equal names for this test, got %q and %q",
			firstPrincipal.Name(), secondPrincipal.Name())
	}

	called := false
	if allowed, err := second.Authorize(context.Background(), firstPrincipal, func(context.Context, netsec.Principal) (bool, error) {
		called = true
		return true, nil
	}); allowed || !errors.Is(err, netsec.ErrPrincipalBinding) || called {
		t.Fatalf("cross-config Authorize = (%v, %v), called=%v", allowed, err, called)
	}
}

func TestPermissionCallbackNotAvailableForCollision(t *testing.T) {
	config := baseConfig()
	config.Template = "{namespace}{serviceAccount}"
	config.Languages = map[netsec.ClaimName]netsec.AllowedLanguage{
		netsec.ClaimNamespace:      netsec.EnumLanguage("a", "ab"),
		netsec.ClaimServiceAccount: netsec.EnumLanguage("bc", "c"),
	}
	policy, err := netsec.Publish(config)
	if policy != nil {
		t.Fatal("collision config was published")
	}
	if !errors.Is(err, netsec.ErrCollision) {
		t.Fatalf("error = %v", err)
	}

	var zero netsec.Principal
	called := false
	if allowed, authErr := policy.Authorize(context.Background(), zero, func(context.Context, netsec.Principal) (bool, error) {
		called = true
		return true, nil
	}); allowed || authErr == nil || called {
		t.Fatalf("nil policy Authorize = (%v, %v), called=%v", allowed, authErr, called)
	}
}

func TestInvalidUTF8BytesAreEncodedBytewise(t *testing.T) {
	if got := netsec.PercentEncodeClaimValue(string([]byte{0xff})); got != "%FF" {
		t.Fatalf("invalid UTF-8 encoding = %q", got)
	}
}
