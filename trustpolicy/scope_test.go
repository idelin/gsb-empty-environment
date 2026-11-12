package trustpolicy

import "testing"

func TestValidateScope(t *testing.T) {
	valid := []string{
		"*",
		"example.com/prod",
		"example.com/prod/app",
		"example.com/*",
		"example.com/prod/*",
		"registry:5000/team/app",
	}
	for _, scope := range valid {
		if err := ValidateScope(scope); err != nil {
			t.Errorf("ValidateScope(%q) = %v, want nil", scope, err)
		}
	}

	invalid := []string{
		"",
		"example.com",               // registry only, no repo
		"example.com//app",          // empty segment
		"example.com/*/app",         // wildcard mid-path
		"exam*le.com/prod",          // wildcard in registry
		"example.com/prod*",         // wildcard not its own segment
		"example.com/prod/*/",       // trailing content after wildcard
		"example.com/prod/*/more/*", // multiple wildcards
	}
	for _, scope := range invalid {
		if err := ValidateScope(scope); err == nil {
			t.Errorf("ValidateScope(%q) = nil, want error", scope)
		}
	}
}

func TestRepositoryOf(t *testing.T) {
	cases := map[string]string{
		"example.com/prod/app":                   "example.com/prod/app",
		"example.com/prod/app:1.0":               "example.com/prod/app",
		"example.com/prod/app@sha256:abc":        "example.com/prod/app",
		"example.com/prod/app:1.0@sha256:abc":    "example.com/prod/app",
		"registry:5000/team/app:latest":          "registry:5000/team/app",
		"registry:5000/team/app@sha256:deadbeef": "registry:5000/team/app",
	}
	for ref, want := range cases {
		got, err := RepositoryOf(ref)
		if err != nil {
			t.Errorf("RepositoryOf(%q) error: %v", ref, err)
			continue
		}
		if got != want {
			t.Errorf("RepositoryOf(%q) = %q, want %q", ref, got, want)
		}
	}
	if _, err := RepositoryOf("noregistry"); err == nil {
		t.Error("RepositoryOf(\"noregistry\") = nil error, want error")
	}
}

func TestScopeMatches(t *testing.T) {
	cases := []struct {
		scope, repo string
		want        bool
	}{
		{"*", "anything.example.com/x/y", true},
		{"example.com/prod/app", "example.com/prod/app", true},
		{"example.com/prod/app", "example.com/prod/app2", false},
		{"example.com/prod/*", "example.com/prod/app", true},
		{"example.com/prod/*", "example.com/prod/team/app", true},
		// Prefix spoofing: the wildcard must not cross the repo boundary.
		{"example.com/prod/*", "example.com/prod-evil/app", false},
		{"example.com/prod/*", "example.com/production/app", false},
		{"example.com/prod", "example.com/prod-evil/app", false},
		{"example.com/*", "other.com/prod/app", false},
	}
	for _, c := range cases {
		if got := scopeMatches(c.scope, c.repo); got != c.want {
			t.Errorf("scopeMatches(%q, %q) = %v, want %v", c.scope, c.repo, got, c.want)
		}
	}
}
