package netsec

import (
	"errors"
	"testing"
)

func canonicalUser() Record {
	return Record{
		"id":          "u-1",
		"name":        "Ada",
		"email":       "ada@example.org",
		"tenantID":    "t-9",
		"roles":       []string{"admin"},
		"permissions": []string{"users.manage"},
	}
}

func newTestRouter() *Router {
	return NewRouter(DefaultRegistry(), func(capability string) (Record, error) {
		return canonicalUser(), nil
	})
}

func v1User() *Identity { return &Identity{UserID: "u-1"} }
func v2Admin() *Identity {
	return &Identity{UserID: "u-2", TenantID: "t-9", Roles: []string{"admin"}}
}

// A logged-in v1 caller must not reach v2-only capabilities, and a plain v1
// read must never expose the sensitive fields introduced for v2.
func TestV1PrivilegeEscalationDenied(t *testing.T) {
	r := newTestRouter()

	_, err := r.Serve(&Request{Path: "/v1/users/u-1", Identity: v1User()}, "users.read.sensitive")
	if !errors.Is(err, ErrCapabilityDenied) {
		t.Fatalf("v1 sensitive capability: got %v, want ErrCapabilityDenied", err)
	}

	rec, err := r.Serve(&Request{Path: "/v1/users/u-1", Identity: v1User()}, "users.read")
	if err != nil {
		t.Fatalf("v1 users.read: unexpected error %v", err)
	}
	for _, sensitive := range []string{"tenantID", "roles", "permissions"} {
		if _, leaked := rec[sensitive]; leaked {
			t.Fatalf("v1 response leaked sensitive field %q: %v", sensitive, rec)
		}
	}
	if rec["id"] != "u-1" || rec["name"] != "Ada" {
		t.Fatalf("v1 response missing allowed fields: %v", rec)
	}
}

// Unauthenticated v1 calls are still rejected by the v1 login-only policy.
func TestV1RequiresLogin(t *testing.T) {
	r := newTestRouter()
	_, err := r.Serve(&Request{Path: "/v1/users/u-1"}, "users.read")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("got %v, want ErrUnauthorized", err)
	}
}

// Versions not registered in the registry are rejected before auth or
// backend calls.
func TestUnknownVersionRejected(t *testing.T) {
	r := newTestRouter()
	_, err := r.Serve(&Request{Path: "/v9/users/u-1", Identity: v2Admin()}, "users.read")
	if !errors.Is(err, ErrUnknownVersion) {
		t.Fatalf("got %v, want ErrUnknownVersion", err)
	}
}

// A version header that disagrees with the path version is a conflict and
// must not silently fall back to either version's policy.
func TestVersionHeaderPathConflict(t *testing.T) {
	r := newTestRouter()
	req := &Request{Path: "/v1/users/u-1", VersionHeader: "v2", Identity: v2Admin()}
	_, err := r.Serve(req, "users.read")
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("got %v, want ErrVersionConflict", err)
	}
}

// A matching version header is accepted and routed through v2's policy.
func TestVersionHeaderPathAgreement(t *testing.T) {
	r := newTestRouter()
	req := &Request{Path: "/v2/users/u-1", VersionHeader: "v2", Identity: v2Admin()}
	if _, err := r.Serve(req, "users.read"); err != nil {
		t.Fatalf("unexpected error %v", err)
	}
}

// A legitimate v2 request passes tenant+role checks and receives the full
// field set, including the sensitive fields hidden from v1.
func TestLegitV2Request(t *testing.T) {
	r := newTestRouter()
	rec, err := r.Serve(&Request{Path: "/v2/users/u-1", Identity: v2Admin()}, "users.read.sensitive")
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	for _, field := range []string{"id", "name", "email", "tenantID", "roles", "permissions"} {
		if _, ok := rec[field]; !ok {
			t.Fatalf("v2 response missing field %q: %v", field, rec)
		}
	}
}

// v2 enforces its own policy: tenant scope and the admin role are mandatory.
func TestV2PolicyEnforced(t *testing.T) {
	r := newTestRouter()

	noTenant := &Identity{UserID: "u-3", Roles: []string{"admin"}}
	if _, err := r.Serve(&Request{Path: "/v2/users/u-1", Identity: noTenant}, "users.read"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("missing tenant: got %v, want ErrForbidden", err)
	}

	noRole := &Identity{UserID: "u-4", TenantID: "t-9", Roles: []string{"viewer"}}
	if _, err := r.Serve(&Request{Path: "/v2/users/u-1", Identity: noRole}, "users.read"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("missing role: got %v, want ErrForbidden", err)
	}
}

// A deprecated version keeps serving only the capabilities it declared; new
// capabilities are denied even to callers that would pass a newer policy.
func TestDeprecatedVersionFrozenCapabilities(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register(&Version{
		Name:         "v0",
		Deprecated:   true,
		Policy:       Policy{RequireLogin: true},
		Capabilities: map[string]bool{"users.read": true},
		FieldMap:     map[string]string{"id": "id"},
	})
	r := NewRouter(reg, func(capability string) (Record, error) {
		return canonicalUser(), nil
	})

	rec, err := r.Serve(&Request{Path: "/v0/users/u-1", Identity: v1User()}, "users.read")
	if err != nil {
		t.Fatalf("declared capability on deprecated version: unexpected error %v", err)
	}
	if len(rec) != 1 || rec["id"] != "u-1" {
		t.Fatalf("deprecated version must expose only declared fields: %v", rec)
	}

	_, err = r.Serve(&Request{Path: "/v0/users/u-1", Identity: v1User()}, "users.manage")
	if !errors.Is(err, ErrCapabilityDenied) {
		t.Fatalf("undeclared capability on deprecated version: got %v, want ErrCapabilityDenied", err)
	}
}
