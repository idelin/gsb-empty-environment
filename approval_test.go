package netsec_test

import (
	"encoding/json"
	"errors"
	"net/url"
	"reflect"
	"testing"

	"example.org/netsec/q0403"
)

func testApproval(t *testing.T) *netsec.Approval {
	t.Helper()

	approval, err := netsec.NewApproval([]netsec.ResourceGrant{
		netsec.NewResourceGrant("https://api.example.com/", "read", "write", "admin"),
		netsec.NewResourceGrant("https://files.example.com/", "read", "write"),
		netsec.NewResourceGrant("https://reports.example.com/", "read"),
	})
	if err != nil {
		t.Fatalf("NewApproval() error = %v", err)
	}
	return approval
}

func TestTokenRequestRejectsResourceSuperset(t *testing.T) {
	t.Parallel()

	approval := testApproval(t)
	_, err := approval.IssueToken(netsec.TokenRequest{
		Resources: []string{
			"https://api.example.com/",
			"https://attacker.example.com/",
		},
	})
	if !netsec.IsInvalidTarget(err) {
		t.Fatalf("superset request error = %v, want invalid_target", err)
	}
}

func TestTokenRequestFromRepeatedFormValues(t *testing.T) {
	t.Parallel()

	approval := testApproval(t)
	values := url.Values{
		"resource": {
			"https://api.example.com/",
			"https://files.example.com/",
		},
		"scope": {"read"},
	}
	token, err := approval.IssueTokenFromValues(values)
	if err != nil {
		t.Fatalf("IssueTokenFromValues() error = %v", err)
	}
	if len(token.Audience) != 2 {
		t.Fatalf("audience = %#v, want two resources", token.Audience)
	}
	if !reflect.DeepEqual(token.Scope, []string{"read"}) {
		t.Fatalf("scope = %#v, want [read]", token.Scope)
	}
}

func TestTokenRequestRejectsFragment(t *testing.T) {
	t.Parallel()

	approval := testApproval(t)
	_, err := approval.IssueToken(netsec.TokenRequest{
		Resources: []string{"https://api.example.com/#fragment"},
	})
	if !netsec.IsInvalidTarget(err) {
		t.Fatalf("fragment request error = %v, want invalid_target", err)
	}
}

func TestTokenRequestRejectsUnspecifiedMultipleResources(t *testing.T) {
	t.Parallel()

	approval := testApproval(t)
	_, err := approval.IssueToken(netsec.TokenRequest{})
	if !netsec.IsInvalidTarget(err) {
		t.Fatalf("unspecified multiple resources error = %v, want invalid_target", err)
	}
}

func TestTokenRequestDefaultsToSoleApprovedResource(t *testing.T) {
	t.Parallel()

	approval, err := netsec.NewApproval([]netsec.ResourceGrant{
		netsec.NewResourceGrant("https://api.example.com/", "read", "write"),
	})
	if err != nil {
		t.Fatalf("NewApproval() error = %v", err)
	}

	token, err := approval.IssueToken(netsec.TokenRequest{})
	if err != nil {
		t.Fatalf("IssueToken() error = %v", err)
	}
	if len(token.Audience) != 1 || token.Audience[0] != "https://api.example.com/" {
		t.Fatalf("default audience = %#v", token.Audience)
	}
	if !reflect.DeepEqual(token.Scope, []string{"read", "write"}) {
		t.Fatalf("default scope = %#v", token.Scope)
	}
}

func TestAudienceIsExactlyRequestedResources(t *testing.T) {
	t.Parallel()

	approval := testApproval(t)
	token, err := approval.IssueToken(netsec.TokenRequest{
		Resources: []string{
			"https://files.example.com/",
			"https://api.example.com/",
		},
	})
	if err != nil {
		t.Fatalf("IssueToken() error = %v", err)
	}

	wantResources := []string{
		"https://files.example.com/",
		"https://api.example.com/",
	}
	if !reflect.DeepEqual(token.Resources, wantResources) {
		t.Fatalf("token resources = %#v, want %#v", token.Resources, wantResources)
	}
	if !reflect.DeepEqual([]string(token.Audience), wantResources) {
		t.Fatalf("token audience = %#v, want %#v", token.Audience, wantResources)
	}
	if len(token.Audience) != 2 {
		t.Fatalf("audience has %d members, want exactly 2", len(token.Audience))
	}
	if !reflect.DeepEqual(token.Scope, []string{"read", "write"}) {
		t.Fatalf("scope intersection = %#v, want [read write]", token.Scope)
	}

	encoded, err := json.Marshal(token)
	if err != nil {
		t.Fatalf("json.Marshal(token) error = %v", err)
	}
	var payload struct {
		Audience []string `json:"aud"`
		Scope    string   `json:"scope"`
	}
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("unmarshal token JSON: %v", err)
	}
	if !reflect.DeepEqual(payload.Audience, wantResources) {
		t.Fatalf("encoded aud = %#v, want %#v", payload.Audience, wantResources)
	}
	if payload.Scope != "read write" {
		t.Fatalf("encoded scope = %q, want %q", payload.Scope, "read write")
	}
}

func TestSingleResourceScopeAndJSONAudience(t *testing.T) {
	t.Parallel()

	approval := testApproval(t)
	token, err := approval.IssueToken(netsec.TokenRequest{
		Resources: []string{"https://reports.example.com/"},
	})
	if err != nil {
		t.Fatalf("IssueToken() error = %v", err)
	}
	if !reflect.DeepEqual(token.Scope, []string{"read"}) {
		t.Fatalf("scope = %#v, want [read]", token.Scope)
	}

	encoded, err := json.Marshal(token)
	if err != nil {
		t.Fatalf("json.Marshal(token) error = %v", err)
	}
	var payload struct {
		Audience string `json:"aud"`
	}
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("unmarshal token JSON: %v", err)
	}
	if payload.Audience != "https://reports.example.com/" {
		t.Fatalf("encoded aud = %q", payload.Audience)
	}

	var decoded netsec.Token
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(token) error = %v", err)
	}
	if len(decoded.Audience) != 1 || decoded.Audience[0] != "https://reports.example.com/" {
		t.Fatalf("decoded audience = %#v", decoded.Audience)
	}
	if !reflect.DeepEqual(decoded.Scope, []string{"read"}) {
		t.Fatalf("decoded scope = %#v", decoded.Scope)
	}
}

func TestTokenRequestMayNotBroadenScope(t *testing.T) {
	t.Parallel()

	approval := testApproval(t)
	_, err := approval.IssueToken(netsec.TokenRequest{
		Resources: []string{"https://reports.example.com/"},
		Scopes:    []string{"read", "write"},
	})
	if err == nil {
		t.Fatal("IssueToken() accepted unapproved scope")
	}
	var oauthErr *netsec.Error
	if !errors.As(err, &oauthErr) || oauthErr.Code != netsec.InvalidScope {
		t.Fatalf("scope error = %v, want invalid_scope", err)
	}
}

func TestRefreshCannotAddResource(t *testing.T) {
	t.Parallel()

	approval, err := netsec.NewApproval([]netsec.ResourceGrant{
		netsec.NewResourceGrant("https://api.example.com/", "read"),
		netsec.NewResourceGrant("https://files.example.com/", "read"),
	})
	if err != nil {
		t.Fatalf("NewApproval() error = %v", err)
	}

	refresh := netsec.NewRefreshGrant(approval)
	_, err = refresh.Issue(netsec.TokenRequest{
		Resources: []string{"https://new.example.com/"},
	})
	if !netsec.IsInvalidTarget(err) {
		t.Fatalf("refresh expansion error = %v, want invalid_target", err)
	}

	token, err := refresh.Issue(netsec.TokenRequest{
		Resources: []string{"https://files.example.com/"},
	})
	if err != nil {
		t.Fatalf("refresh within original approval error = %v", err)
	}
	if len(token.Audience) != 1 || token.Audience[0] != "https://files.example.com/" {
		t.Fatalf("refresh audience = %#v", token.Audience)
	}
	if !reflect.DeepEqual(token.Scope, []string{"read"}) {
		t.Fatalf("refresh scope = %#v", token.Scope)
	}
}

func TestRefreshCannotExpandAudienceBeyondPreviousTokenOrApproval(t *testing.T) {
	t.Parallel()

	approval := testApproval(t)
	token, err := approval.IssueToken(netsec.TokenRequest{
		Resources: []string{"https://reports.example.com/"},
	})
	if err != nil {
		t.Fatalf("IssueToken() error = %v", err)
	}
	_ = token

	_, err = netsec.RefreshToken(approval, netsec.TokenRequest{
		Resources: []string{"https://api.example.com/"},
	})
	if err != nil {
		t.Fatalf("refresh within original approval should be allowed: %v", err)
	}

	_, err = netsec.RefreshToken(approval, netsec.TokenRequest{
		Resources: []string{"https://attacker.example.com/"},
	})
	if !netsec.IsInvalidTarget(err) {
		t.Fatalf("refresh expansion error = %v, want invalid_target", err)
	}
}

func TestRefreshCannotExpandScope(t *testing.T) {
	t.Parallel()

	approval := testApproval(t)
	refresh := netsec.NewRefreshGrant(approval)
	_, err := refresh.Issue(netsec.TokenRequest{
		Resources: []string{"https://reports.example.com/"},
		Scopes:    []string{"read", "write"},
	})
	var oauthErr *netsec.Error
	if !errors.As(err, &oauthErr) || oauthErr.Code != netsec.InvalidScope {
		t.Fatalf("refresh scope expansion error = %v, want invalid_scope", err)
	}
}

func TestApprovalRejectsFragmentResource(t *testing.T) {
	t.Parallel()

	_, err := netsec.NewApproval([]netsec.ResourceGrant{
		netsec.NewResourceGrant("https://api.example.com/#fragment", "read"),
	})
	if !netsec.IsInvalidTarget(err) {
		t.Fatalf("fragment approval error = %v, want invalid_target", err)
	}
}

func TestApprovalSnapshotIsImmutable(t *testing.T) {
	t.Parallel()

	scopes := []string{"read", "write"}
	approval, err := netsec.NewApproval([]netsec.ResourceGrant{
		{Resource: "https://api.example.com/", Scopes: scopes},
	})
	if err != nil {
		t.Fatalf("NewApproval() error = %v", err)
	}
	scopes[0] = "admin"

	token, err := approval.IssueToken(netsec.TokenRequest{})
	if err != nil {
		t.Fatalf("IssueToken() error = %v", err)
	}
	if !reflect.DeepEqual(token.Scope, []string{"read", "write"}) {
		t.Fatalf("approval changed after source slice mutation: %#v", token.Scope)
	}
}
