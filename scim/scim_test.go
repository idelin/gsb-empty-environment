package scim

import (
	"errors"
	"strings"
	"testing"
)

const (
	tenantA = "tenant-a"
	tenantB = "tenant-b"
)

func directory() []User {
	return []User{
		{ID: "u-1", TenantID: tenantA, UserName: "alice", DisplayName: "Alice A", Active: true,
			Emails: []string{"alice@a.example"}, Groups: []string{"eng"}},
		{ID: "u-2", TenantID: tenantA, UserName: "bob", DisplayName: "Bob A", Active: false,
			Emails: []string{"bob@a.example"}, Groups: []string{"ops"}},
		{ID: "u-3", TenantID: tenantA, UserName: "carol", DisplayName: "Carol A", Active: true,
			Emails: []string{"carol@a.example", "carol.alt@a.example"}, Groups: []string{"eng", "sec"}},
		{ID: "u-9", TenantID: tenantB, UserName: "mallory", DisplayName: "Mallory B", Active: true,
			Emails: []string{"mallory@b.example"}, Groups: []string{"eng"}},
		{ID: "u-10", TenantID: tenantB, UserName: "alice", DisplayName: "Alice B", Active: true,
			Emails: []string{"alice@b.example"}, Groups: []string{"hr"}},
	}
}

func newAuth() *Authorizer { return NewAuthorizer(DefaultPolicy()) }

func query(t *testing.T, tenant, filter string) *Result {
	t.Helper()
	res, err := newAuth().Query(tenant, directory(), Query{Filter: filter})
	if err != nil {
		t.Fatalf("Query(%q) failed: %v", filter, err)
	}
	return res
}

func expectCode(t *testing.T, err error, code ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error %q, got nil", code)
	}
	var se *Error
	if !errors.As(err, &se) || se.Code != code {
		t.Fatalf("expected error code %q, got %v", code, err)
	}
}

func assertOnlyTenant(t *testing.T, res *Result, tenant string) {
	t.Helper()
	users := directory()
	allowed := map[string]bool{}
	for _, u := range users {
		if u.TenantID == tenant {
			allowed[u.ID] = true
		}
	}
	for _, r := range res.Resources {
		id, _ := r["id"].(string)
		if !allowed[id] {
			t.Fatalf("result contains cross-tenant user %q: %v", id, r)
		}
	}
}

func TestORInjectionCannotEscapeTenant(t *testing.T) {
	// Classic injection: broaden an exact match with OR branches.
	res := query(t, tenantA, `userName eq "alice" or userName eq "mallory"`)
	// "mallory" exists only in tenant-b; scoped evaluation must not
	// return her even though the OR branch matches her userName.
	if res.TotalResults != 1 || res.Resources[0]["id"] != "u-1" {
		t.Fatalf("OR injection altered scoped results: %+v", res.Resources)
	}
	assertOnlyTenant(t, res, tenantA)
	for _, r := range res.Resources {
		if r["userName"] == "mallory" {
			t.Fatalf("OR injection leaked cross-tenant user: %v", r)
		}
	}

	res = query(t, tenantA, `(userName eq "alice" or userName eq "bob") or userName eq "carol"`)
	if res.TotalResults != 3 {
		t.Fatalf("expected 3 tenant-a users, got %d", res.TotalResults)
	}
	assertOnlyTenant(t, res, tenantA)

	res = query(t, tenantA, `userName eq "alice" or (displayName sw "m" or displayName sw "a")`)
	assertOnlyTenant(t, res, tenantA)

	// OR fan-out beyond the policy limit is rejected.
	var b strings.Builder
	b.WriteString(`userName eq "u0"`)
	for i := 1; i <= 9; i++ {
		b.WriteString(` or userName eq "u` + strings.Repeat("x", 1) + `"`)
	}
	_, err := newAuth().Query(tenantA, directory(), Query{Filter: b.String()})
	expectCode(t, err, ErrCodeTooComplex)
}

func TestUnauthorizedAttributeRejected(t *testing.T) {
	cases := []struct {
		filter string
		code   ErrorCode
	}{
		{`tenantId eq "tenant-b"`, ErrCodeUnauthorizedAttr}, // tenant pivot
		{`password eq "hunter2"`, ErrCodeUnauthorizedAttr},  // unknown attr
		{`meta.lastModified gt "2020-01-01"`, ErrCodeUnauthorizedAttr},
		{`userName co "ali"`, ErrCodeOperatorNotAllowed}, // substring on sensitive attr
		{`userName pr`, ErrCodeOperatorNotAllowed},       // presence scan on sensitive attr
		{`emails[value co "@b.example"]`, ErrCodeOperatorNotAllowed},
		{`active eq "yes"`, ErrCodeInvalidValue}, // type confusion
		{`userName eq ""`, ErrCodeInvalidValue},
	}
	for _, tc := range cases {
		_, err := newAuth().Query(tenantA, directory(), Query{Filter: tc.filter})
		expectCode(t, err, tc.code)
	}
}

func TestNegationCannotWidenResults(t *testing.T) {
	// Bare top-level NOT is rejected by default.
	_, err := newAuth().Query(tenantA, directory(), Query{Filter: `not (userName eq "alice")`})
	expectCode(t, err, ErrCodeNegationNotAllowed)

	// NOT inside OR is rejected.
	_, err = newAuth().Query(tenantA, directory(),
		Query{Filter: `userName eq "alice" or not (userName eq "bob")`})
	expectCode(t, err, ErrCodeNegationNotAllowed)

	// NOT wrapping a logical expression is rejected.
	_, err = newAuth().Query(tenantA, directory(),
		Query{Filter: `active eq true and not (userName eq "alice" or userName eq "bob")`})
	expectCode(t, err, ErrCodeNegationNotAllowed)

	// NOT under AND is allowed and stays inside the tenant scope.
	res := query(t, tenantA, `active eq true and not (userName eq "alice")`)
	if res.TotalResults != 1 || res.Resources[0]["userName"] != "carol" {
		t.Fatalf("unexpected NOT result: %+v", res.Resources)
	}
	assertOnlyTenant(t, res, tenantA)
}

func TestCrossTenantIDReturnsNothing(t *testing.T) {
	res := query(t, tenantA, `id eq "u-9"`) // u-9 belongs to tenant-b
	if res.TotalResults != 0 || len(res.Resources) != 0 {
		t.Fatalf("cross-tenant id lookup returned %d results", res.TotalResults)
	}
	res = query(t, tenantA, `id eq "u-1"`)
	if res.TotalResults != 1 || res.Resources[0]["userName"] != "alice" {
		t.Fatalf("own-tenant id lookup failed: %+v", res.Resources)
	}
}

func TestLegitimateExactQueries(t *testing.T) {
	res := query(t, tenantA, `userName eq "alice"`)
	if res.TotalResults != 1 || res.Resources[0]["id"] != "u-1" {
		t.Fatalf("exact userName query failed: %+v", res.Resources)
	}

	// Case-insensitive per SCIM defaults.
	res = query(t, tenantA, `USERNAME EQ "ALICE"`)
	if res.TotalResults != 1 {
		t.Fatalf("case-insensitive query failed: %+v", res.Resources)
	}

	// ValuePath on multi-valued sensitive attribute.
	res = query(t, tenantA, `emails[value eq "carol.alt@a.example"]`)
	if res.TotalResults != 1 || res.Resources[0]["userName"] != "carol" {
		t.Fatalf("emails valuePath query failed: %+v", res.Resources)
	}

	// Compound AND with groups.
	res = query(t, tenantA, `active eq true and groups eq "sec"`)
	if res.TotalResults != 1 || res.Resources[0]["userName"] != "carol" {
		t.Fatalf("groups query failed: %+v", res.Resources)
	}

	// URN-qualified attribute path resolves to the same rule.
	res = query(t, tenantA, `urn:ietf:params:scim:schemas:core:2.0:User:userName eq "bob"`)
	if res.TotalResults != 1 || res.Resources[0]["id"] != "u-2" {
		t.Fatalf("URN-qualified query failed: %+v", res.Resources)
	}
}

func TestAttributeSelectionAndVisibility(t *testing.T) {
	// Explicit selection returns only the requested attributes.
	res := query(t, tenantA, `userName eq "carol"`)
	_ = res
	res, err := newAuth().Query(tenantA, directory(), Query{
		Filter:     `userName eq "carol"`,
		Attributes: []string{"userName", "emails"},
	})
	if err != nil {
		t.Fatalf("attribute selection failed: %v", err)
	}
	got := res.Resources[0]
	if len(got) != 2 || got["userName"] != "carol" {
		t.Fatalf("projection mismatch: %v", got)
	}
	if _, ok := got["groups"]; ok {
		t.Fatalf("unrequested attribute leaked: %v", got)
	}

	// Selecting an unauthorized attribute is rejected.
	_, err = newAuth().Query(tenantA, directory(), Query{Attributes: []string{"tenantId"}})
	expectCode(t, err, ErrCodeInvalidProjection)
	_, err = newAuth().Query(tenantA, directory(), Query{ExcludedAttributes: []string{"password"}})
	expectCode(t, err, ErrCodeInvalidProjection)

	// Exclusion removes the attribute from the default projection.
	res, err = newAuth().Query(tenantA, directory(), Query{
		Filter:             `userName eq "alice"`,
		ExcludedAttributes: []string{"emails", "groups"},
	})
	if err != nil {
		t.Fatalf("exclusion failed: %v", err)
	}
	if _, ok := res.Resources[0]["emails"]; ok {
		t.Fatalf("excluded attribute present: %v", res.Resources[0])
	}

	// No projection ever exposes the tenant boundary attribute.
	for _, r := range res.Resources {
		if _, ok := r["tenantId"]; ok {
			t.Fatalf("tenantId leaked in projection: %v", r)
		}
	}
}

func TestSortAndPaginationRestrictions(t *testing.T) {
	res, err := newAuth().Query(tenantA, directory(), Query{
		SortBy: "userName", SortOrder: "descending",
	})
	if err != nil {
		t.Fatalf("sort failed: %v", err)
	}
	if res.Resources[0]["userName"] != "carol" {
		t.Fatalf("descending sort broken: %v", res.Resources)
	}
	assertOnlyTenant(t, res, tenantA)

	// Sorting by a non-sortable attribute is rejected.
	_, err = newAuth().Query(tenantA, directory(), Query{SortBy: "id"})
	expectCode(t, err, ErrCodeInvalidSort)
	_, err = newAuth().Query(tenantA, directory(), Query{SortBy: "emails"})
	expectCode(t, err, ErrCodeInvalidSort)

	// Page size is capped by the policy.
	res, err = newAuth().Query(tenantA, directory(), Query{Count: 1000})
	if err != nil {
		t.Fatalf("pagination failed: %v", err)
	}
	if res.ItemsPerPage > DefaultPolicy().MaxPageSize {
		t.Fatalf("page size cap not enforced: %d", res.ItemsPerPage)
	}

	// Invalid pagination parameters are rejected.
	_, err = newAuth().Query(tenantA, directory(), Query{StartIndex: -1})
	expectCode(t, err, ErrCodeInvalidPagination)

	// startIndex beyond the scoped set yields an empty page, not an error
	// and certainly not another tenant's data.
	res, err = newAuth().Query(tenantA, directory(), Query{StartIndex: 99})
	if err != nil {
		t.Fatalf("out-of-range startIndex should not error: %v", err)
	}
	if len(res.Resources) != 0 {
		t.Fatalf("expected empty page, got %v", res.Resources)
	}
}

func TestParseErrors(t *testing.T) {
	for _, f := range []string{
		`userName eq`, `userName xx "a"`, `(userName eq "a"`,
		`userName eq "a" extra`, `not userName eq "a"`,
		`emails[value eq "a"`, `userName eq "unterminated`,
	} {
		if _, err := Parse(f); err == nil {
			t.Fatalf("expected parse error for %q", f)
		}
	}
}
