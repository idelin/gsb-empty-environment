package netsec

import (
	"errors"
	"testing"
)

func TestLiteralMatchesOnlyExactName(t *testing.T) {
	p := Pattern{Kind: KindTopic, Type: TypeLiteral, Value: "tenant-a"}
	m, err := p.Compile('-')
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	cases := map[string]bool{
		"tenant-a":        true,
		"tenant-a-secret": false,
		"tenant-ab":       false,
		"tenant":          false,
		"TENANT-A":        false,
		"x-tenant-a":      false,
		"tenant-a-orders": false,
	}
	for name, want := range cases {
		if got := m(name); got != want {
			t.Errorf("literal match(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestPrefixRespectsDelimiterBoundary(t *testing.T) {
	// The headline attack: "tenant-a" must not reach "tenant-a-secret"
	// through a bare string-prefix/contains check.
	p := Pattern{Kind: KindTopic, Type: TypePrefix, Value: "tenant-a"}
	m, err := p.Compile('-')
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	cases := map[string]bool{
		"tenant-a":            true, // exact name still matches
		"tenant-a-orders":     true, // boundary is a delimiter
		"tenant-a-billing-v1": true,
		"tenant-a-secret":     true,  // segment-boundary child; the headline policy would instead use a literal or a narrower prefix
		"tenant-ab":           false, // similar prefix, no boundary
		"tenant-az":           false,
		"tenant":              false,
		"x-tenant-a":          false,
	}
	for name, want := range cases {
		if got := m(name); got != want {
			t.Errorf("prefix match(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestPrefixWithTrailingDelimiterNormalizes(t *testing.T) {
	m, err := Pattern{Kind: KindTopic, Type: TypePrefix, Value: "tenant-a-"}.Compile('-')
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if m("tenant-a") {
		t.Error("trailing-delimiter prefix must not match the bare name")
	}
	if !m("tenant-a-secret") {
		// Explicit "tenant-a-" rule deliberately scopes the whole namespace.
		t.Error("trailing-delimiter prefix should match names on that boundary")
	}
	if m("tenant-ab") {
		t.Error("similar prefix without boundary must not match")
	}
}

func TestPrefixWithSlashDelimiter(t *testing.T) {
	m, err := Pattern{Kind: KindTopic, Type: TypePrefix, Value: "tenants/a"}.Compile('/')
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	cases := map[string]bool{
		"tenants/a":        true,
		"tenants/a/orders": true,
		"tenants/a-secret": false, // '-' is not the boundary with '/' delim
		"tenants/ab":       false,
	}
	for name, want := range cases {
		if got := m(name); got != want {
			t.Errorf("slash prefix match(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestWildcardMatchesPerSegment(t *testing.T) {
	m, err := Pattern{Kind: KindTopic, Type: TypeWildcard, Value: "tenant-*-orders-*"}.Compile('-')
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	cases := map[string]bool{
		"tenant-a-orders-1":   true,
		"tenant-abc-orders-x": true,
		"tenant-a-orders":     false, // wrong segment count
		"tenant-a-secret-1":   false,
		"tenant-a-orders-1-x": false,
	}
	for name, want := range cases {
		if got := m(name); got != want {
			t.Errorf("wildcard match(%q) = %v, want %v", name, got, want)
		}
	}

	// A star never crosses the delimiter.
	star, err := Pattern{Kind: KindTopic, Type: TypeWildcard, Value: "*"}.Compile('-')
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if star("tenant-a") {
		t.Error("single-segment wildcard must not span delimiters")
	}
	if !star("anything") {
		t.Error("single-segment wildcard should match a one-segment name")
	}
}

func TestMatchingIsCaseSensitive(t *testing.T) {
	lit, _ := Pattern{Kind: KindTopic, Type: TypeLiteral, Value: "Tenant-A"}.Compile('-')
	if lit("tenant-a") {
		t.Error("matching must be case sensitive")
	}
	pre, _ := Pattern{Kind: KindTopic, Type: TypePrefix, Value: "Tenant"}.Compile('-')
	if pre("tenant-a") {
		t.Error("prefix matching must be case sensitive")
	}
}

func TestEmptyAndMalformedNamesRejected(t *testing.T) {
	badResources := []Resource{
		{Kind: KindTopic, Name: ""},
		{Kind: KindGroup, Name: "-leading"},
		{Kind: KindGroup, Name: "trailing-"},
		{Kind: KindTransaction, Name: "double--delim"},
		{Kind: ResourceKind(99), Name: "x"},
	}
	for _, r := range badResources {
		if err := r.Valid('-'); err == nil {
			t.Errorf("resource %+v expected validation error", r)
		}
	}

	badPatterns := []Pattern{
		{Kind: KindTopic, Type: TypeLiteral, Value: ""},
		{Kind: KindTopic, Type: TypePrefix, Value: ""},
		{Kind: KindTopic, Type: TypeWildcard, Value: ""},
		{Kind: KindTopic, Type: TypePrefix, Value: "-"},
		{Kind: KindTopic, Type: TypeWildcard, Value: "a--b"},
		{Kind: KindTopic, Type: PatternType(99), Value: "a"},
		{Kind: ResourceKind(99), Type: TypeLiteral, Value: "a"},
	}
	for _, p := range badPatterns {
		if err := p.Valid('-'); err == nil {
			t.Errorf("pattern %+v expected validation error", p)
		}
	}

	if !errors.Is(mustValidateErr(t, Resource{Kind: KindTopic, Name: ""}), ErrEmptyName) {
		t.Error("empty resource name should wrap ErrEmptyName")
	}
}

func mustValidateErr(t *testing.T, r Resource) error {
	t.Helper()
	err := r.Valid('-')
	if err == nil {
		t.Fatal("expected error")
	}
	return err
}

func TestKindMismatchDoesNotAuthorize(t *testing.T) {
	a := NewAuthorizer('-')
	_, err := a.ReplaceACL([]Entry{
		{Principal: "svc", Effect: EffectAllow, Actions: ActionRead, Pattern: Pattern{
			Kind: KindTopic, Type: TypeLiteral, Value: "tenant-a",
		}},
	})
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	// Same string, different resource kinds: the topic rule must not leak
	// into group or transaction authorization.
	if err := a.AuthorizeGroup("svc", "tenant-a", ActionRead); !errors.Is(err, ErrNotAuthorized) {
		t.Errorf("group check = %v, want not authorized", err)
	}
	if err := a.AuthorizeTransaction("svc", "tenant-a", ActionWrite); !errors.Is(err, ErrNotAuthorized) {
		t.Errorf("transaction check = %v, want not authorized", err)
	}
	if err := a.AuthorizeTopic("svc", "tenant-a", ActionRead); err != nil {
		t.Errorf("topic check = %v, want allowed", err)
	}
}
