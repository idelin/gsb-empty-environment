package scim

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// TestEscalation_TenantAdminIntoSystemGroup 复现核心攻击：低权限租户管理员
// 构造 PATCH /Groups/sys-admins，试图把自己加入系统管理员组。
// 目标组属于平台租户，租户边界先将其作为 NotFound 拒收，组状态不变。
func TestEscalation_TenantAdminIntoSystemGroup(t *testing.T) {
	e := newSecurityFixture(t)
	ctx := context.Background()
	alice := securityFixtureAlice()

	attack := PatchRequest{
		Schemas: []string{"urn:ietf:params:scim:api:messages:2.0:PatchOp"},
		Operations: []PatchOperation{{
			Op:   "add",
			Path: "members",
			Value: []any{
				Resource{"value": "u-alice", "type": "User"},
			},
		}},
	}

	_, err := e.ApplyPatch(ctx, alice, "Group", "g-sys", attack)
	if err == nil {
		t.Fatal("privilege escalation must be rejected")
	}
	if !errors.Is(err, ErrNotFound) || !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("system group access should be hidden NotFound+TenantMismatch, got %v", err)
	}

	g, _ := e.Store.Get("Group", "g-sys")
	members := g["members"].([]any)
	if len(members) != 1 {
		t.Fatalf("system group membership changed: %v", members)
	}
	if members[0].(Resource)["value"] != "u-root" {
		t.Fatalf("unexpected surviving member: %v", members[0])
	}
}

// TestEscalation_SameTenantPrivilegedGroup 验证即使特权组位于本租户，
// 没有 group-admin / 系统管理员身份也不能改动其成员边界。
func TestEscalation_SameTenantPrivilegedGroup(t *testing.T) {
	e := newSecurityFixture(t)
	ctx := context.Background()
	alice := securityFixtureAlice()

	_, err := e.Patch(ctx, alice, "Group", "g-a-priv",
		PatchOperation{
			Op:    "add",
			Path:  "members",
			Value: []any{Resource{"value": "u-alice"}},
		})
	if !errors.Is(err, ErrPrivilegedGroup) {
		t.Fatalf("want ErrPrivilegedGroup, got %v", err)
	}

	g, _ := e.Store.Get("Group", "g-a-priv")
	if len(g["members"].([]any)) != 0 {
		t.Fatalf("privileged group mutated after rejection: %v", g["members"])
	}

	sysAdmin := Caller{TenantID: "tenant-a", SubjectID: "u-root", SystemAdmin: true}
	if _, err := e.Patch(ctx, sysAdmin, "Group", "g-a-priv",
		PatchOperation{
			Op:    "add",
			Path:  "members",
			Value: []any{Resource{"value": "u-alice"}},
		}); err != nil {
		t.Fatalf("system admin should manage privileged group: %v", err)
	}
}

// TestEscalation_CrossTenantMemberIntoOwnGroup 验证向本租户组挂载其它租户
// 用户（含 $ref 与过滤器改引用形式）会被跨租户边界拒绝。
func TestEscalation_CrossTenantMemberIntoOwnGroup(t *testing.T) {
	e := newSecurityFixture(t)
	ctx := context.Background()
	alice := securityFixtureAlice()

	attacks := []PatchOperation{
		{
			Op:    "add",
			Path:  "members",
			Value: []any{Resource{"value": "u-bob"}},
		},
		{
			Op:   "add",
			Path: "members",
			Value: []any{
				Resource{"$ref": "https://idp.example/scim/v2/Users/u-bob"},
			},
		},
		{
			Op:    "replace",
			Path:  "members",
			Value: []any{Resource{"value": "u-bob"}, Resource{"value": "u-carol"}},
		},
		{
			Op:    "replace",
			Path:  `members[value eq "u-carol"].value`,
			Value: "u-dave",
		},
	}
	for i, op := range attacks {
		_, err := e.Patch(ctx, alice, "Group", "g-a-team", op)
		if !errors.Is(err, ErrCrossTenantMember) {
			t.Fatalf("attack %d (%s) should fail with ErrCrossTenantMember, got %v", i, op.Path, err)
		}
	}

	g, _ := e.Store.Get("Group", "g-a-team")
	members := g["members"].([]any)
	if len(members) != 1 || members[0].(Resource)["value"] != "u-carol" {
		t.Fatalf("group must remain untouched after cross-tenant attempts: %v", members)
	}
}

// TestEscalation_RoleGrantBlocked 验证通过 PATCH roles 自授管理员角色被拒。
func TestEscalation_RoleGrantBlocked(t *testing.T) {
	e := newSecurityFixture(t)
	ctx := context.Background()
	alice := securityFixtureAlice()

	_, err := e.Patch(ctx, alice, "User", "u-alice",
		PatchOperation{
			Op:   "replace",
			Path: "roles",
			Value: []any{
				Resource{"value": "sysadmin"},
				Resource{"value": "tenant-admin"},
			},
		})
	if !errors.Is(err, ErrPrivilegedAttribute) {
		t.Fatalf("role self-grant should be ErrPrivilegedAttribute, got %v", err)
	}

	u, _ := e.Store.Get("User", "u-alice")
	if _, present := u["roles"]; present {
		t.Fatalf("roles must not be persisted after rejection: %v", u["roles"])
	}
}

// TestEscalation_PrivilegedGroupRenameBlocked 验证特权组的非成员写操作同样被拒。
func TestEscalation_PrivilegedGroupRenameBlocked(t *testing.T) {
	e := newSecurityFixture(t)
	_, err := e.Patch(context.Background(), securityFixtureAlice(), "Group", "g-a-priv",
		PatchOperation{Op: "replace", Path: "displayName", Value: "Owned Admins"})
	if !errors.Is(err, ErrPrivilegedGroup) {
		t.Fatalf("any write on privileged group should be denied, got %v", err)
	}
}

// TestPatch_PathSubFilterSensitiveChecked 验证 PATCH 路径方括号内的
// 子过滤器不能用于探测敏感属性。
func TestPatch_PathSubFilterSensitiveChecked(t *testing.T) {
	e := newSecurityFixture(t)
	_, err := e.Patch(context.Background(), securityFixtureAlice(), "Group", "g-a-team",
		PatchOperation{
			Op:    "replace",
			Path:  `members[roles pr].displayName`,
			Value: "pwn",
		})
	if !errors.Is(err, ErrSensitiveAttribute) {
		t.Fatalf("sensitive path sub-filter should be denied, got %v", err)
	}
}

// TestEscalation_PatchRequestJSONShape 验证 RFC 7644 JSON 信封可直接反序列化并拦截。
func TestEscalation_PatchRequestJSONShape(t *testing.T) {
	e := newSecurityFixture(t)
	raw := []byte(`{
		"schemas": ["urn:ietf:params:scim:api:messages:2.0:PatchOp"],
		"Operations": [{
			"op": "add",
			"path": "members",
			"value": [{"value": "u-bob", "type": "User"}]
		}]
	}`)
	var req PatchRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatal(err)
	}
	_, err := e.ApplyPatch(context.Background(), securityFixtureAlice(), "Group", "g-a-team", req)
	if !errors.Is(err, ErrCrossTenantMember) {
		t.Fatalf("JSON PATCH attack should be blocked, got %v", err)
	}
}
