package scim

import (
	"context"
	"errors"
	"testing"
)

func TestParsePatchPath(t *testing.T) {
	cases := []struct {
		raw       string
		root      string
		hasFilter bool
		sub       string
	}{
		{"displayName", "displayName", false, ""},
		{"name.familyName", "name", false, ""},
		{"members", "members", false, ""},
		{`members[value eq "u-1"]`, "members", true, ""},
		{`members[value eq "u-1"].displayName`, "members", true, "displayName"},
		{`emails[type eq "work"].value`, "emails", true, "value"},
	}
	for _, tc := range cases {
		pp, err := ParsePatchPath(tc.raw)
		if err != nil {
			t.Fatalf("ParsePatchPath(%q): %v", tc.raw, err)
		}
		if pp.Attr.Root() != tc.root {
			t.Fatalf("%q: root = %q want %q", tc.raw, pp.Attr.Root(), tc.root)
		}
		if (pp.Filter != nil) != tc.hasFilter {
			t.Fatalf("%q: filter presence = %v want %v", tc.raw, pp.Filter != nil, tc.hasFilter)
		}
		if tc.sub != "" && (len(pp.SubPath) == 0 || pp.SubPath[0] != tc.sub) {
			t.Fatalf("%q: subpath = %v want %q", tc.raw, pp.SubPath, tc.sub)
		}
	}

	for _, bad := range []string{
		`members[value eq]`,
		`members[value eq "x"`,
		`foo..bar`,
		`9bad`,
	} {
		if _, err := ParsePatchPath(bad); !errors.Is(err, ErrInvalidPatchPath) {
			t.Fatalf("%q should be ErrInvalidPatchPath, got %v", bad, err)
		}
	}
}

func TestPatch_ImmutableAttributeRejected(t *testing.T) {
	e := newSecurityFixture(t)
	ctx := context.Background()
	alice := securityFixtureAlice()

	_, err := e.Patch(ctx, alice, "User", "u-alice",
		PatchOperation{Op: "replace", Path: "tenantId", Value: "tenant-b"})
	if !errors.Is(err, ErrImmutableAttribute) {
		t.Fatalf("want ErrImmutableAttribute, got %v", err)
	}

	r, _ := e.Store.Get("User", "u-alice")
	if r["tenantId"] != "tenant-a" {
		t.Fatalf("immutable tenantId was mutated: %v", r["tenantId"])
	}

	// immutable 对系统管理员同样不可通过 PATCH 修改。
	admin := Caller{TenantID: "tenant-a", SystemAdmin: true}
	_, err = e.Patch(ctx, admin, "User", "u-alice",
		PatchOperation{Op: "replace", Path: "tenantId", Value: "tenant-c"})
	if !errors.Is(err, ErrImmutableAttribute) {
		t.Fatalf("system admin must also hit ErrImmutableAttribute, got %v", err)
	}
}

func TestPatch_ReadOnlyAttributesRejected(t *testing.T) {
	e := newSecurityFixture(t)
	ctx := context.Background()
	alice := securityFixtureAlice()

	cases := []struct {
		rt, id, path string
		value        any
	}{
		{"User", "u-alice", "id", "u-root"},
		{"User", "u-alice", "meta", Resource{"created": "now"}},
		{"User", "u-alice", "groups", []any{Resource{"value": "g-sys"}}},
		{"Group", "g-a-team", "privileged", true},
	}
	for _, tc := range cases {
		_, err := e.Patch(ctx, alice, tc.rt, tc.id,
			PatchOperation{Op: "replace", Path: tc.path, Value: tc.value})
		if !errors.Is(err, ErrReadOnlyAttribute) {
			t.Fatalf("%s should be readOnly, got %v", tc.path, err)
		}
	}

	g, _ := e.Store.Get("Group", "g-a-team")
	if _, ok := g["privileged"]; ok {
		t.Fatal("privileged flag must not be mass-assignable")
	}
}

func TestPatch_UnknownAttributeRejected(t *testing.T) {
	e := newSecurityFixture(t)
	_, err := e.Patch(context.Background(), securityFixtureAlice(), "User", "u-alice",
		PatchOperation{Op: "add", Path: "isSystemAdmin", Value: true})
	if !errors.Is(err, ErrUnknownAttribute) {
		t.Fatalf("want ErrUnknownAttribute (mass assignment defense), got %v", err)
	}
}

func TestPatch_PrivilegedAttributesRequireRole(t *testing.T) {
	e := newSecurityFixture(t)
	ctx := context.Background()
	alice := securityFixtureAlice()

	for _, op := range []PatchOperation{
		{Op: "replace", Path: "active", Value: false},
		{Op: "replace", Path: "roles", Value: []any{Resource{"value": "sysadmin"}}},
		{Op: "replace", Path: "externalId", Value: "ext-root"},
	} {
		_, err := e.Patch(ctx, alice, "User", "u-alice", op)
		if !errors.Is(err, ErrPrivilegedAttribute) {
			t.Fatalf("%s should require elevated role, got %v", op.Path, err)
		}
	}

	// identity-admin 可以修改这些属性。
	admin := Caller{TenantID: "tenant-a", SubjectID: "u-admin", Roles: []string{"identity-admin"}}
	updated, err := e.Patch(ctx, admin, "User", "u-alice",
		PatchOperation{Op: "replace", Path: "active", Value: false})
	if err != nil {
		t.Fatalf("identity-admin should update active: %v", err)
	}
	if updated["active"] != false {
		t.Fatalf("active not updated: %v", updated["active"])
	}
}

func TestPatch_HappyPaths(t *testing.T) {
	e := newSecurityFixture(t)
	ctx := context.Background()
	alice := securityFixtureAlice()

	updated, err := e.Patch(ctx, alice, "User", "u-alice",
		PatchOperation{Op: "replace", Path: "displayName", Value: "Alice A"})
	if err != nil {
		t.Fatal(err)
	}
	if updated["displayName"] != "Alice A" {
		t.Fatal("displayName not replaced")
	}

	_, err = e.Patch(ctx, alice, "User", "u-alice",
		PatchOperation{Op: "replace", Path: "name.familyName", Value: "Liddell"})
	if err != nil {
		t.Fatal(err)
	}
	r, _ := e.Store.Get("User", "u-alice")
	if name, _ := r["name"].(Resource); name["familyName"] != "Liddell" {
		t.Fatalf("nested familyName not set: %#v", r["name"])
	}

	// 同租户成员可以挂载。
	updated, err = e.Patch(ctx, alice, "Group", "g-a-team",
		PatchOperation{Op: "add", Path: "members", Value: []any{Resource{"value": "u-alice"}}})
	if err != nil {
		t.Fatalf("same-tenant member add should succeed: %v", err)
	}
	if members := updated["members"].([]any); len(members) != 2 {
		t.Fatalf("expected 2 members, got %d", len(members))
	}

	// 过滤器定位成员并修改其 display 子属性（字符串值不得误判为引用）。
	_, err = e.Patch(ctx, alice, "Group", "g-a-team",
		PatchOperation{
			Op:    "replace",
			Path:  `members[value eq "u-alice"].displayName`,
			Value: "Alice W",
		})
	if err != nil {
		t.Fatalf("member displayName sub-patch should succeed: %v", err)
	}

	// 过滤器定位并移除 carol。
	updated, err = e.Patch(ctx, alice, "Group", "g-a-team",
		PatchOperation{Op: "remove", Path: `members[value eq "u-carol"]`})
	if err != nil {
		t.Fatalf("filtered remove should succeed: %v", err)
	}
	members := updated["members"].([]any)
	if len(members) != 1 {
		t.Fatalf("carol removed, one member left, got %d", len(members))
	}
	if m := members[0].(Resource); m["value"] != "u-alice" {
		t.Fatalf("remaining member should be alice, got %v", m["value"])
	}
}

func TestPatch_AtomicRollback(t *testing.T) {
	e := newSecurityFixture(t)
	ctx := context.Background()
	alice := securityFixtureAlice()

	// op1 合法且会先改写 displayName；op2 跨租户挂载必须失败，
	// 整个 PATCH 回滚，displayName 与 members 均保持原样。
	_, err := e.ApplyPatch(ctx, alice, "Group", "g-a-team", PatchRequest{
		Operations: []PatchOperation{
			{Op: "replace", Path: "displayName", Value: "Hacked Team"},
			{Op: "add", Path: "members", Value: []any{Resource{"value": "u-bob"}}},
		},
	})
	if !errors.Is(err, ErrCrossTenantMember) {
		t.Fatalf("want ErrCrossTenantMember, got %v", err)
	}

	g, _ := e.Store.Get("Group", "g-a-team")
	if g["displayName"] != "Team A" {
		t.Fatalf("rollback failed, displayName = %v", g["displayName"])
	}
	members := g["members"].([]any)
	if len(members) != 1 {
		t.Fatalf("rollback failed, members = %v", members)
	}
	if m := members[0].(Resource); m["value"] != "u-carol" {
		t.Fatalf("original member changed: %v", m)
	}

	// 不可变属性失败同样回滚前序操作。
	_, err = e.Patch(ctx, alice, "User", "u-alice",
		PatchOperation{Op: "replace", Path: "displayName", Value: "Temporary"},
		PatchOperation{Op: "replace", Path: "tenantId", Value: "tenant-b"},
	)
	if !errors.Is(err, ErrImmutableAttribute) {
		t.Fatalf("want ErrImmutableAttribute, got %v", err)
	}
	u, _ := e.Store.Get("User", "u-alice")
	if u["displayName"] != nil {
		t.Fatalf("earlier op must roll back, displayName = %v", u["displayName"])
	}
}

func TestStore_TransactionRollback(t *testing.T) {
	s := NewStore()
	s.Put("User", Resource{"id": "u1", "tenantId": "t1", "v": "old"})

	boom := errors.New("boom")
	err := s.Transact(func(tx *Tx) error {
		r, _ := tx.Get("User", "u1")
		r["v"] = "mutated"
		tx.Put("User", Resource{"id": "u2", "tenantId": "t1"})
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("want boom, got %v", err)
	}
	r, ok := s.Get("User", "u1")
	if !ok || r["v"] != "old" {
		t.Fatalf("snapshot rollback failed for u1: %v", r)
	}
	if _, ok := s.Get("User", "u2"); ok {
		t.Fatal("rolled-back transaction must not leave u2 behind")
	}
}
