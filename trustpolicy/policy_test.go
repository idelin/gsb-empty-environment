package trustpolicy

import (
	"errors"
	"testing"
)

func testStore(t *testing.T, policies []Policy) *Store {
	t.Helper()
	s, err := NewStore(policies)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}

func TestSelectSpecificScopeBeatsGlobal(t *testing.T) {
	s := testStore(t, []Policy{
		{Name: "global-audit", RegistryScopes: []string{"*"}, VerificationLevel: LevelAudit},
		{Name: "prod-strict", RegistryScopes: []string{"example.com/prod/*"}, VerificationLevel: LevelStrict},
	})
	p, err := s.Select("example.com/prod/app:1.0")
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if p.Name != "prod-strict" {
		t.Fatalf("Select picked %q, want prod-strict", p.Name)
	}
}

func TestSelectFallsBackToGlobal(t *testing.T) {
	s := testStore(t, []Policy{
		{Name: "global-permissive", RegistryScopes: []string{"*"}, VerificationLevel: LevelPermissive},
		{Name: "prod-strict", RegistryScopes: []string{"example.com/prod/*"}, VerificationLevel: LevelStrict},
	})
	p, err := s.Select("other.io/random/app@sha256:abc")
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if p.Name != "global-permissive" {
		t.Fatalf("Select picked %q, want global-permissive", p.Name)
	}
}

func TestSelectNoMatch(t *testing.T) {
	s := testStore(t, []Policy{
		{Name: "prod-strict", RegistryScopes: []string{"example.com/prod/*"}, VerificationLevel: LevelStrict},
	})
	if _, err := s.Select("other.io/app:1.0"); !errors.Is(err, ErrNoMatchingPolicy) {
		t.Fatalf("Select error = %v, want ErrNoMatchingPolicy", err)
	}
}

func TestSelectOverlappingScopesIsConfigError(t *testing.T) {
	s := testStore(t, []Policy{
		{Name: "prod-wildcard", RegistryScopes: []string{"example.com/prod/*"}, VerificationLevel: LevelPermissive},
		{Name: "prod-app-exact", RegistryScopes: []string{"example.com/prod/app"}, VerificationLevel: LevelStrict},
		{Name: "global-audit", RegistryScopes: []string{"*"}, VerificationLevel: LevelAudit},
	})
	_, err := s.Select("example.com/prod/app:1.0")
	var overlap *OverlapError
	if !errors.As(err, &overlap) {
		t.Fatalf("Select error = %v, want *OverlapError", err)
	}
	if len(overlap.Policies) != 2 {
		t.Fatalf("OverlapError.Policies = %v, want 2 entries", overlap.Policies)
	}

	// A repo covered by only one specific scope still selects cleanly.
	p, err := s.Select("example.com/prod/other:1.0")
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if p.Name != "prod-wildcard" {
		t.Fatalf("Select picked %q, want prod-wildcard", p.Name)
	}
}

func TestSelectPrefixSpoofingDoesNotMatch(t *testing.T) {
	s := testStore(t, []Policy{
		{Name: "prod-permissive", RegistryScopes: []string{"example.com/prod/*"}, VerificationLevel: LevelPermissive},
		{Name: "global-strict", RegistryScopes: []string{"*"}, VerificationLevel: LevelStrict},
	})
	// prod-evil must not inherit the permissive prod policy; it falls back
	// to the strict global policy.
	p, err := s.Select("example.com/prod-evil/app:1.0")
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if p.Name != "global-strict" {
		t.Fatalf("Select picked %q for prod-evil, want global-strict", p.Name)
	}
}

func TestNewStoreRejectsInvalidPolicy(t *testing.T) {
	if _, err := NewStore([]Policy{
		{Name: "bad", RegistryScopes: []string{"example.com/*/app"}, VerificationLevel: LevelStrict},
	}); err == nil {
		t.Fatal("NewStore accepted a mid-path wildcard scope")
	}
	if _, err := NewStore([]Policy{
		{Name: "bad", RegistryScopes: []string{"example.com/app"}, VerificationLevel: "yolo"},
	}); err == nil {
		t.Fatal("NewStore accepted an unknown verification level")
	}
}
