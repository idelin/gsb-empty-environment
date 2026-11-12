package universallink

import (
	"encoding/json"
	"errors"
	"testing"
)

func strptr(s string) *string { return &s }

const ownAppID = "ABCD1234.com.example.Links"

// testAssociation exercises the full component machinery: an exclude rule in
// front of open rules, a query-constrained rule and a fragment rule.
func testAssociation() *Association {
	return &Association{
		AppLinks: AppLinks{
			Details: []Detail{
				{
					AppIDs: []string{ownAppID},
					Components: []Component{
						{Path: strptr("/account*"), Exclude: true},
						{Path: strptr("/admin/*"), Exclude: true},
						{Path: strptr("/share/*"), Query: map[string]string{"token": "t-*"}},
						{Path: strptr("/share/*")},
						{Path: strptr("/fragment"), Fragment: strptr("sec-*")},
						{Path: strptr("*")},
					},
				},
			},
		},
	}
}

func TestDotSegmentAttacks(t *testing.T) {
	matcher, err := NewMatcher(ownAppID, testAssociation())
	if err != nil {
		t.Fatalf("NewMatcher: %v", err)
	}

	tests := []struct {
		name     string
		raw      string
		decision Decision
	}{
		{"dot segment into excluded account", "https://example.com/share/../account", DecisionExcluded},
		{"dot segment chain into excluded account", "https://example.com/share/public/../../account", DecisionExcluded},
		{"dot segment keeps public path", "https://example.com/share/./open", DecisionOpen},
		{"plain share opens", "https://example.com/share/open", DecisionOpen},
		{"plain account excluded", "https://example.com/account/settings", DecisionExcluded},
		{"admin subtree excluded", "https://example.com/admin/users", DecisionExcluded},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := matcher.Decide(tc.raw)
			if err != nil {
				t.Fatalf("Decide(%q) error: %v", tc.raw, err)
			}
			if got != tc.decision {
				t.Fatalf("Decide(%q) = %v, want %v", tc.raw, got, tc.decision)
			}
		})
	}
}

func TestExcludeOrdering(t *testing.T) {
	// First matching component wins even when a later component would open.
	association := &Association{
		AppLinks: AppLinks{
			Details: []Detail{
				{
					AppIDs: []string{ownAppID},
					Components: []Component{
						{Path: strptr("/x*"), Exclude: true},
						{Path: strptr("*")},
					},
				},
			},
		},
	}
	matcher, err := NewMatcher(ownAppID, association)
	if err != nil {
		t.Fatalf("NewMatcher: %v", err)
	}

	if open, err := matcher.ShouldOpen("https://example.com/xyz"); err != nil || open {
		t.Fatalf("excluded-first case = %v, %v; want false, nil", open, err)
	}
	if open, err := matcher.ShouldOpen("https://example.com/public"); err != nil || !open {
		t.Fatalf("fallthrough case = %v, %v; want true, nil", open, err)
	}
}

func TestQueryComponentMatching(t *testing.T) {
	association := &Association{
		AppLinks: AppLinks{
			Details: []Detail{
				{
					AppIDs: []string{ownAppID},
					Components: []Component{
						{Path: strptr("/share/*"), Query: map[string]string{"token": "t-*"}},
					},
				},
			},
		},
	}
	matcher, err := NewMatcher(ownAppID, association)
	if err != nil {
		t.Fatalf("NewMatcher: %v", err)
	}

	tests := []struct {
		name     string
		raw      string
		decision Decision
	}{
		{"query pattern matches", "https://example.com/share/x?token=t-abc", DecisionOpen},
		{"empty tail matches star", "https://example.com/share/x?token=t-", DecisionOpen},
		{"extra query keys allowed", "https://example.com/share/x?token=t-1&extra=2", DecisionOpen},
		{"wrong query value", "https://example.com/share/x?token=evil", DecisionNoMatch},
		{"missing required key", "https://example.com/share/x?other=1", DecisionNoMatch},
		{"path mismatch despite query", "https://example.com/other/x?token=t-1", DecisionNoMatch},
		{"repeated key one match is enough", "https://example.com/share/x?token=bad&token=t-good", DecisionOpen},
		{"multiple constrained keys", "https://example.com/share/x?token=t-1&user=bob", DecisionNoMatch},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Add the second constraint only for the last case.
			association.AppLinks.Details[0].Components[0].Query = map[string]string{"token": "t-*"}
			if tc.name == "multiple constrained keys" {
				association.AppLinks.Details[0].Components[0].Query["user"] = "alice"
			}
			got, err := matcher.Decide(tc.raw)
			if err != nil {
				t.Fatalf("Decide(%q) error: %v", tc.raw, err)
			}
			if got != tc.decision {
				t.Fatalf("Decide(%q) = %v, want %v", tc.raw, got, tc.decision)
			}
		})
	}
}

func TestFragmentComponentMatching(t *testing.T) {
	association := &Association{
		AppLinks: AppLinks{
			Details: []Detail{
				{
					AppIDs: []string{ownAppID},
					Components: []Component{
						{Path: strptr("/fragment"), Fragment: strptr("sec-*")},
					},
				},
			},
		},
	}
	matcher, err := NewMatcher(ownAppID, association)
	if err != nil {
		t.Fatalf("NewMatcher: %v", err)
	}

	tests := []struct {
		name     string
		raw      string
		decision Decision
	}{
		{"fragment matches", "https://example.com/fragment#sec-42", DecisionOpen},
		{"fragment mismatch", "https://example.com/fragment#other", DecisionNoMatch},
		{"missing fragment", "https://example.com/fragment", DecisionNoMatch},
		{"path mismatch", "https://example.com/other#sec-1", DecisionNoMatch},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := matcher.Decide(tc.raw)
			if err != nil {
				t.Fatalf("Decide(%q) error: %v", tc.raw, err)
			}
			if got != tc.decision {
				t.Fatalf("Decide(%q) = %v, want %v", tc.raw, got, tc.decision)
			}
		})
	}
}

func TestQuestionMarkWildcard(t *testing.T) {
	association := &Association{
		AppLinks: AppLinks{
			Details: []Detail{
				{
					AppIDs: []string{ownAppID},
					Components: []Component{
						{Path: strptr("/home?")},
					},
				},
			},
		},
	}
	matcher, err := NewMatcher(ownAppID, association)
	if err != nil {
		t.Fatalf("NewMatcher: %v", err)
	}

	tests := []struct {
		raw      string
		decision Decision
	}{
		{"https://example.com/home1", DecisionOpen},
		{"https://example.com/home", DecisionNoMatch},
		{"https://example.com/home12", DecisionNoMatch},
	}

	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			got, err := matcher.Decide(tc.raw)
			if err != nil {
				t.Fatalf("Decide(%q) error: %v", tc.raw, err)
			}
			if got != tc.decision {
				t.Fatalf("Decide(%q) = %v, want %v", tc.raw, got, tc.decision)
			}
		})
	}
}

func TestRejectedURLs(t *testing.T) {
	matcher, err := NewMatcher(ownAppID, testAssociation())
	if err != nil {
		t.Fatalf("NewMatcher: %v", err)
	}

	tests := []struct {
		name string
		raw  string
		want error
	}{
		{"encoded slash", "https://example.com/share%2f../account", ErrEncodedSlash},
		{"encoded uppercase slash", "https://example.com/a%2Fb", ErrEncodedSlash},
		{"duplicate slash", "https://example.com/share//account", ErrDuplicateSlash},
		{"leading double slash", "https://example.com//account", ErrDuplicateSlash},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			open, err := matcher.ShouldOpen(tc.raw)
			if !errors.Is(err, tc.want) {
				t.Fatalf("ShouldOpen(%q) error = %v, want %v", tc.raw, err, tc.want)
			}
			if open {
				t.Fatalf("ShouldOpen(%q) = true, want false on rejected URL", tc.raw)
			}
		})
	}
}

func TestTeamIDFiltering(t *testing.T) {
	association := &Association{
		AppLinks: AppLinks{
			Details: []Detail{
				{
					AppIDs:     []string{"OTHER9.com.other.App"},
					Components: []Component{{Path: strptr("*")}},
				},
				{
					AppIDs:     []string{"ABCD1234.com.example.Links"},
					Components: []Component{{Path: strptr("/only-here")}},
				},
				{
					AppIDs:     []string{"no-dot-appid", "ABCD1234.com.example.Links"},
					Components: []Component{{Path: strptr("/multi")}},
				},
			},
		},
	}
	matcher, err := NewMatcher(ownAppID, association)
	if err != nil {
		t.Fatalf("NewMatcher: %v", err)
	}

	if open, err := matcher.ShouldOpen("https://example.com/only-here"); err != nil || !open {
		t.Fatalf("own-team rule = %v, %v; want true", open, err)
	}
	if open, err := matcher.ShouldOpen("https://example.com/multi"); err != nil || !open {
		t.Fatalf("multi-appID detail = %v, %v; want true", open, err)
	}
	if decision, err := matcher.Decide("https://example.com/foreign"); err != nil || decision != DecisionNoMatch {
		t.Fatalf("foreign-team detail = %v, %v; want DecisionNoMatch", decision, err)
	}
}

func TestInvalidOwnAppID(t *testing.T) {
	if _, err := NewMatcher("no-dot-here", testAssociation()); err == nil {
		t.Fatal("NewMatcher with dotless appID must return an error")
	}
	if _, err := NewMatcher(ownAppID, nil); err == nil {
		t.Fatal("NewMatcher with nil association must return an error")
	}
}

func TestMalformedQueryFailsSafe(t *testing.T) {
	matcher, err := NewMatcher(ownAppID, testAssociation())
	if err != nil {
		t.Fatalf("NewMatcher: %v", err)
	}

	// A component with a query constraint never matches a query string that
	// cannot be decoded; evaluation continues with later components.
	decision, err := matcher.Decide("https://example.com/share/x?token=%zz")
	if err != nil {
		t.Fatalf("Decide returned error: %v", err)
	}
	if decision != DecisionOpen {
		t.Fatalf("decision = %v, want DecisionOpen via the unconstrained /share/* rule", decision)
	}
}

func TestOpaqueURLRejected(t *testing.T) {
	matcher, err := NewMatcher(ownAppID, testAssociation())
	if err != nil {
		t.Fatalf("NewMatcher: %v", err)
	}
	if open, err := matcher.ShouldOpen("mailto:user@example.com"); err == nil || open {
		t.Fatalf("opaque URL = %v, %v; want false with error", open, err)
	}
}

func TestUnmarshalAASA(t *testing.T) {
	raw := []byte(`{
		"applinks": {
			"details": [
				{
					"appIDs": ["ABCD1234.com.example.Links"],
					"components": [
						{"/": "/account*", "exclude": true, "comment": "protected"},
						{"/": "/share/*", "?": {"token": "t-*"}},
						{"/": "*", "#": "frag?"}
					]
				}
			]
		}
	}`)

	var association Association
	if err := json.Unmarshal(raw, &association); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	matcher, err := NewMatcher(ownAppID, &association)
	if err != nil {
		t.Fatalf("NewMatcher: %v", err)
	}

	tests := []struct {
		raw      string
		decision Decision
	}{
		{"https://example.com/account", DecisionExcluded},
		{"https://example.com/share/x?token=t-9", DecisionOpen},
		{"https://example.com/other#frag1", DecisionOpen},
		{"https://example.com/other#frag12", DecisionNoMatch},
	}

	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			got, err := matcher.Decide(tc.raw)
			if err != nil {
				t.Fatalf("Decide(%q) error: %v", tc.raw, err)
			}
			if got != tc.decision {
				t.Fatalf("Decide(%q) = %v, want %v", tc.raw, got, tc.decision)
			}
		})
	}
}
