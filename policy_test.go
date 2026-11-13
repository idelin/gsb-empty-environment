package netsec

import (
	"errors"
	"testing"
)

func TestEvaluateRejectsTraversalOutsideProviderRoot(t *testing.T) {
	t.Parallel()

	policy := Policy{
		Root: "/public",
		Rules: []Rule{
			{PathPrefix: "/public", Permission: PermissionRead},
			{Path: "/private/db", Permission: PermissionReadWrite, Deny: true},
		},
		Grants: []GrantRule{{PathPrefix: "/public"}},
	}

	for _, request := range []string{
		"content://auth/public/..%2Fprivate/db",
		"content://auth/public/%2e%2e%2fprivate/db",
		"content://auth/public/%252e%252e%252fprivate/db",
	} {
		decision, err := policy.Evaluate(request)
		if !errors.Is(err, ErrEscapesRoot) {
			t.Fatalf("Evaluate(%q) error = %v, want ErrEscapesRoot", request, err)
		}
		if !decision.Denied {
			t.Fatalf("Evaluate(%q) Denied = false, want true", request)
		}
		if decision.GrantURI {
			t.Fatalf("Evaluate(%q) applied a grant outside the provider root", request)
		}
	}
}

func TestTraversalCannotInheritPublicPermission(t *testing.T) {
	t.Parallel()

	policy := Policy{
		Root: "/",
		Rules: []Rule{
			{Name: "public-read", PathPrefix: "/public", Permission: PermissionRead},
			{Name: "private-write", Path: "/private/db", Permission: PermissionWrite},
		},
	}

	decision, err := policy.Evaluate("content://auth/public/..%2Fprivate/db")
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if decision.Path != "/private/db" {
		t.Fatalf("Path = %q, want /private/db", decision.Path)
	}
	if decision.Denied {
		t.Fatal("unexpected deny")
	}
	if decision.Required != PermissionWrite {
		t.Fatalf("Required = %v, want write", decision.Required)
	}
	if len(decision.MatchedRules) != 1 || decision.MatchedRules[0] != 1 {
		t.Fatalf("MatchedRules = %v, want [1] (public prefix must not match)", decision.MatchedRules)
	}
	if decision.Permits(PermissionRead) {
		t.Fatal("read-only permission must not satisfy the private write rule")
	}
	if !decision.Permits(PermissionWrite) {
		t.Fatal("write permission should satisfy the private write rule")
	}
}

func TestAllMatchingRulesAndDenyWin(t *testing.T) {
	t.Parallel()

	policy := Policy{
		Root: "/",
		Rules: []Rule{
			{Name: "public-read", PathPrefix: "/public", Permission: PermissionRead},
			{Name: "secret-deny", Path: "/public/secret/db", Deny: true},
			{Name: "secret-write", Path: "/public/secret/db", Permission: PermissionWrite},
		},
	}

	decision, err := policy.Evaluate("/public/secret/db")
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !decision.Denied {
		t.Fatal("Denied = false, want true")
	}
	if decision.Required != PermissionReadWrite {
		t.Fatalf("Required = %v, want read-write", decision.Required)
	}
	if len(decision.MatchedRules) != 3 {
		t.Fatalf("MatchedRules = %v, want all three rules", decision.MatchedRules)
	}
	if decision.Permits(PermissionReadWrite) {
		t.Fatal("deny must win over all permissions")
	}
}

func TestHigherPermissionRequirementWins(t *testing.T) {
	t.Parallel()

	policy := Policy{
		Root: "/public",
		Rules: []Rule{
			{PathPrefix: "/public", Permission: PermissionRead},
			{Path: "/public/admin", Permission: PermissionWrite},
		},
	}

	decision, err := policy.Evaluate("/public/admin")
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if decision.Denied {
		t.Fatal("unexpected deny")
	}
	if decision.Required != PermissionReadWrite {
		t.Fatalf("Required = %v, want read-write", decision.Required)
	}
	if len(decision.MatchedRules) != 2 {
		t.Fatalf("MatchedRules = %v, want both rules", decision.MatchedRules)
	}
}

func TestPrefixRequiresSegmentBoundary(t *testing.T) {
	t.Parallel()

	policy := Policy{
		Root: "/",
		Rules: []Rule{
			{Name: "public", PathPrefix: "/public", Permission: PermissionRead},
			{Name: "publicity", PathPrefix: "/publicity", Permission: PermissionWrite},
		},
		Grants: []GrantRule{{PathPrefix: "/public"}},
	}

	decision, err := policy.Evaluate("/publicity/db")
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(decision.MatchedRules) != 1 || decision.MatchedRules[0] != 1 {
		t.Fatalf("MatchedRules = %v, want only the /publicity rule", decision.MatchedRules)
	}
	if decision.Required != PermissionWrite {
		t.Fatalf("Required = %v, want write", decision.Required)
	}
}

func TestGrantURIPermissionUsesCanonicalPath(t *testing.T) {
	t.Parallel()

	policy := Policy{
		Root:   "/",
		Grants: []GrantRule{{PathPrefix: "/public"}},
	}

	allowed, err := policy.Evaluate("/public/reports/2026.txt")
	if err != nil {
		t.Fatalf("Evaluate legal path: %v", err)
	}
	if !allowed.GrantURI || !allowed.Permits(PermissionNone) {
		t.Fatal("grant-uri-permission should cover the legal /public path")
	}

	for _, request := range []string{
		"/public/..%2Fprivate/db",
		"/public/%252e%252e%252fprivate/db",
	} {
		decision, err := policy.Evaluate(request)
		if err != nil {
			t.Fatalf("Evaluate(%q): %v", request, err)
		}
		if decision.Path != "/private/db" {
			t.Fatalf("Path = %q, want /private/db", decision.Path)
		}
		if decision.GrantURI {
			t.Fatalf("grant prefix matched the encoded traversal %q", request)
		}
	}
}

func TestLegalPathGetsReadPermission(t *testing.T) {
	t.Parallel()

	policy := Policy{
		Root: "/public",
		Rules: []Rule{
			{PathPrefix: "/public", Permission: PermissionRead},
			{Path: "/public/private/db", Permission: PermissionReadWrite, Deny: true},
		},
	}

	decision, err := policy.Evaluate("content://auth/public/reports/2026.txt")
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if decision.Denied || !decision.Matched {
		t.Fatalf("decision = %+v, want matched and not denied", decision)
	}
	if decision.Path != "/public/reports/2026.txt" {
		t.Fatalf("Path = %q", decision.Path)
	}
	if decision.Required != PermissionRead {
		t.Fatalf("Required = %v, want read", decision.Required)
	}
	if !decision.Permits(PermissionRead) {
		t.Fatal("read permission should permit the legal path")
	}
}

func TestMalformedRequestIsDenied(t *testing.T) {
	t.Parallel()

	policy := Policy{Root: "/public"}
	decision, err := policy.Evaluate("/public/%2")
	if !errors.Is(err, ErrInvalidPercentEncoding) {
		t.Fatalf("error = %v, want ErrInvalidPercentEncoding", err)
	}
	if !decision.Denied {
		t.Fatal("Denied = false, want true")
	}
}

func TestParseContentURI(t *testing.T) {
	t.Parallel()

	uri, err := ParseContentURI("content://auth/public/..%2Fprivate/db?x=1#frag")
	if err != nil {
		t.Fatalf("ParseContentURI: %v", err)
	}
	if uri.Authority != "auth" {
		t.Fatalf("Authority = %q, want auth", uri.Authority)
	}
	if uri.Path != "/public/..%2Fprivate/db" {
		t.Fatalf("Path = %q", uri.Path)
	}
}
