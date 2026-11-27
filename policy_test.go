package scim

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestList_TenantIsolation(t *testing.T) {
	e := newSecurityFixture(t)
	ctx := context.Background()

	alice := securityFixtureAlice()
	users, err := e.List(ctx, alice, "User", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 {
		t.Fatalf("alice should see 2 tenant-a users, got %d", len(users))
	}
	for _, u := range users {
		if u["tenantId"] != "tenant-a" {
			t.Fatalf("leaked cross-tenant user: %v", u)
		}
	}

	bob := Caller{TenantID: "tenant-b", SubjectID: "u-bob", TenantAdmin: true}
	users, err = e.List(ctx, bob, "User", `userName sw "b"`)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0]["id"] != "u-bob" {
		t.Fatalf("tenant-b prefix query should only return bob, got %v", users)
	}

	groups, err := e.List(ctx, alice, "Group", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 {
		t.Fatalf("alice should see g-a-priv and g-a-team, got %d groups", len(groups))
	}
}

func TestList_CrossTenantGetHiddenAsNotFound(t *testing.T) {
	e := newSecurityFixture(t)
	_, err := e.Get(context.Background(), securityFixtureAlice(), "User", "u-bob")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant get should be ErrNotFound, got %v", err)
	}
	if !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("error should also wrap ErrTenantMismatch for auditing, got %v", err)
	}
}

func TestSecureFilter_BindsConjunction(t *testing.T) {
	e := NewEngine()
	c := Caller{TenantID: "tenant-a", SubjectID: "u1"}

	secured, err := e.SecureFilter(c, `userName eq "alice" or active eq true`)
	if err != nil {
		t.Fatal(err)
	}
	s := secured.String()
	if !strings.HasPrefix(s, "((") || !strings.HasSuffix(s, `and tenantId eq "tenant-a")`) {
		t.Fatalf("tenant predicate must be outer AND with user filter parenthesized: %s", s)
	}

	// 空 filter 只绑定租户谓词。
	secured, err = e.SecureFilter(c, "  ")
	if err != nil {
		t.Fatal(err)
	}
	if secured.String() != `tenantId eq "tenant-a"` {
		t.Fatalf("unexpected empty secured filter: %s", secured)
	}
}

func TestSecureFilter_SystemAdminWithoutTenant(t *testing.T) {
	e := NewEngine()
	admin := Caller{SystemAdmin: true, SubjectID: "u-root"}
	expr, err := e.SecureFilter(admin, `userName pr`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(expr.String(), "userName pr") || strings.Contains(expr.String(), "tenantId") {
		t.Fatalf("platform admin without tenant should not be scoped: %s", expr)
	}

	plain := Caller{SubjectID: "nobody"}
	if _, err := e.SecureFilter(plain, `userName pr`); !errors.Is(err, ErrTenantScopeRequired) {
		t.Fatalf("caller without tenant should be rejected, got %v", err)
	}
}

func TestAuthorizeFilter_SensitiveAttributes(t *testing.T) {
	e := NewEngine()
	tenantAdmin := Caller{TenantID: "tenant-a", TenantAdmin: true}
	auditor := Caller{TenantID: "tenant-a", Roles: []string{"identity-auditor"}}
	sysAdmin := Caller{TenantID: "__system__", SystemAdmin: true}

	raw := `userName eq "alice" and roles pr`

	if _, err := e.SecureFilter(tenantAdmin, raw); !errors.Is(err, ErrSensitiveAttribute) {
		t.Fatalf("tenant admin must not query roles, got %v", err)
	}
	if _, err := e.SecureFilter(auditor, raw); err != nil {
		t.Fatalf("auditor should read roles: %v", err)
	}
	if _, err := e.SecureFilter(sysAdmin, raw); err != nil {
		t.Fatalf("system admin should read roles: %v", err)
	}

	for _, sensitive := range []string{
		`groups[value pr]`,
		`externalId eq "ext-1"`,
		`urn:ietf:params:scim:schemas:core:2.0:User:roles pr`,
	} {
		if _, err := e.SecureFilter(tenantAdmin, sensitive); !errors.Is(err, ErrSensitiveAttribute) {
			t.Fatalf("%q should be denied as sensitive, got %v", sensitive, err)
		}
	}
}

func TestPolicy_CustomSensitiveAttribute(t *testing.T) {
	p := DefaultPolicy().WithSensitiveAttributes("phoneNumbers")
	e := NewEngine().WithPolicy(p)
	c := Caller{TenantID: "tenant-a"}
	_, err := e.SecureFilter(c, `phoneNumbers[value co "123"]`)
	if !errors.Is(err, ErrSensitiveAttribute) {
		t.Fatalf("custom sensitive attr should be denied, got %v", err)
	}
}
