package scim

import "testing"

// newSecurityFixture 构造跨两个租户加平台租户的身份拓扑：
//
//	tenant-a: u-alice（租户管理员）、u-carol
//	tenant-b: u-bob、u-dave
//	__system__: u-root 与特权组 g-sys
func newSecurityFixture(t *testing.T) *Engine {
	t.Helper()
	e := NewEngine()

	users := []Resource{
		{"id": "u-alice", "tenantId": "tenant-a", "userName": "alice", "active": true},
		{"id": "u-carol", "tenantId": "tenant-a", "userName": "carol", "active": true},
		{"id": "u-bob", "tenantId": "tenant-b", "userName": "bob", "active": true},
		{"id": "u-dave", "tenantId": "tenant-b", "userName": "dave", "active": true},
		{"id": "u-root", "tenantId": "__system__", "userName": "root", "active": true},
	}
	for _, u := range users {
		e.Store.Put("User", u)
	}

	groups := []Resource{
		{
			"id":          "g-sys",
			"tenantId":    "__system__",
			"displayName": "System Administrators",
			"privileged":  true,
			"members": []any{
				Resource{"value": "u-root", "display": "root"},
			},
		},
		{
			"id":          "g-a-priv",
			"tenantId":    "tenant-a",
			"displayName": "Tenant Privileged Operators",
			"privileged":  true,
			"members":     []any{},
		},
		{
			"id":          "g-a-team",
			"tenantId":    "tenant-a",
			"displayName": "Team A",
			"members": []any{
				Resource{"value": "u-carol", "display": "carol"},
			},
		},
		{
			"id":          "g-b-team",
			"tenantId":    "tenant-b",
			"displayName": "Team B",
			"members": []any{
				Resource{"value": "u-bob", "display": "bob"},
			},
		},
	}
	for _, g := range groups {
		e.Store.Put("Group", g)
	}
	return e
}

func securityFixtureAlice() Caller {
	return Caller{TenantID: "tenant-a", SubjectID: "u-alice", TenantAdmin: true}
}
