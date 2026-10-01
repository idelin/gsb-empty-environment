package temporal

import (
	"reflect"
	"testing"
)

// 搭建验收场景：
//   - 系统直接授予 A 角色 R，[0, +inf)，知悉于 0
//   - A 委托 R 给 B，业务 [10, 50)，知悉于 10
//   - B 在 20 委托 R 给 C，业务 [20, 60)，知悉于 20
//   - A 在知悉 30 撤回对 B 的委托，自业务 25 起失效
//
// 角色 R 映射权限 {"doc:read", "doc:write"}。
func setupAcceptance() (*Resolver, int64) {
	r := NewResolver()
	r.SetRolePermissions("R", "doc:read", "doc:write")
	r.GrantRole(Grant{Grantor: "", Grantee: "A", Role: "R",
		ValidFrom: 0, ValidTo: Infinity, RecordedAt: 0})
	ab := r.GrantRole(Grant{Grantor: "A", Grantee: "B", Role: "R",
		ValidFrom: 10, ValidTo: 50, RecordedAt: 10})
	r.GrantRole(Grant{Grantor: "B", Grantee: "C", Role: "R",
		ValidFrom: 20, ValidTo: 60, RecordedAt: 20})
	r.Revoke(ab, 25, 30)
	return r, ab
}

// 验收边界：知悉 35 查询 C 在业务 28 的权限 —— 必须判定已失效。
func TestAcceptance_CRevokedAtAudit35(t *testing.T) {
	r, _ := setupAcceptance()
	if got := r.EffectivePermissions("C", 28, 35); len(got) != 0 {
		t.Fatalf("C @ (vt=28, tt=35) 应无权限, 得到 %v", got)
	}
	if r.HoldsRole("C", "R", 28, 35) {
		t.Fatal("C 在 (28, 35) 不应持有 R")
	}
}

// 撤回尚未知悉时（审计 25 < 30），C 在业务 28 仍持有权限。
func TestRevocationNotYetKnown(t *testing.T) {
	r, _ := setupAcceptance()
	want := []string{"doc:read", "doc:write"}
	if got := r.EffectivePermissions("C", 28, 25); !reflect.DeepEqual(got, want) {
		t.Fatalf("C @ (vt=28, tt=25) 应有权限 %v, 得到 %v", want, got)
	}
}

// 撤回自业务 25 起生效：业务 24（撤回前）即使审计 35 仍有效。
func TestRevocationEffectiveFromValidTime(t *testing.T) {
	r, _ := setupAcceptance()
	want := []string{"doc:read", "doc:write"}
	if got := r.EffectivePermissions("C", 24, 35); !reflect.DeepEqual(got, want) {
		t.Fatalf("C @ (vt=24, tt=35) 应有权限 %v, 得到 %v", want, got)
	}
	if got := r.EffectivePermissions("C", 25, 35); len(got) != 0 {
		t.Fatalf("C @ (vt=25, tt=35) 应无权限（撤回边界，左闭右开）, 得到 %v", got)
	}
}

// 多层委托链过期：B->C 委托区间 [20,60) 虽覆盖业务 55，
// 但上游 A->B 在业务 50 已过期，故 C 在 55 无权限（即使撤回未知悉）。
func TestChainExpiryPropagates(t *testing.T) {
	r, _ := setupAcceptance()
	if got := r.EffectivePermissions("C", 55, 25); len(got) != 0 {
		t.Fatalf("C @ (vt=55, tt=25) 应因上游过期而失效, 得到 %v", got)
	}
	// 边界：业务 49 仍在上游 [10,50) 内，有效。
	want := []string{"doc:read", "doc:write"}
	if got := r.EffectivePermissions("C", 49, 25); !reflect.DeepEqual(got, want) {
		t.Fatalf("C @ (vt=49, tt=25) 应有权限 %v, 得到 %v", want, got)
	}
}

// 委托区间下界：业务 15 时 B 已持有但 B->C 委托（20 起）尚未生效。
func TestDelegationNotYetStarted(t *testing.T) {
	r, _ := setupAcceptance()
	if got := r.EffectivePermissions("C", 15, 25); len(got) != 0 {
		t.Fatalf("C @ (vt=15, tt=25) 应无权限（委托未开始）, 得到 %v", got)
	}
	want := []string{"doc:read", "doc:write"}
	if got := r.EffectivePermissions("B", 15, 25); !reflect.DeepEqual(got, want) {
		t.Fatalf("B @ (vt=15, tt=25) 应有权限 %v, 得到 %v", want, got)
	}
}

// 授权记录本身的知悉时间：审计 5 时 A->B 授权（知悉于 10）尚不可见。
func TestGrantNotYetRecorded(t *testing.T) {
	r, _ := setupAcceptance()
	if got := r.EffectivePermissions("B", 15, 5); len(got) != 0 {
		t.Fatalf("B @ (vt=15, tt=5) 应无权限（授权尚未知悉）, 得到 %v", got)
	}
}

// 无权委托：D 不持有 R，其委托给 E 不生效。
func TestDelegationWithoutHolding(t *testing.T) {
	r := NewResolver()
	r.SetRolePermissions("R", "doc:read")
	r.GrantRole(Grant{Grantor: "D", Grantee: "E", Role: "R",
		ValidFrom: 0, ValidTo: 100, RecordedAt: 0})
	if got := r.EffectivePermissions("E", 10, 10); len(got) != 0 {
		t.Fatalf("E 不应从未持有 R 的 D 处获得权限, 得到 %v", got)
	}
}

// 委托环：X->Y 与 Y->X 互相委托且均无根授权，解析必须终止且无效。
func TestDelegationCycleTerminates(t *testing.T) {
	r := NewResolver()
	r.SetRolePermissions("R", "doc:read")
	r.GrantRole(Grant{Grantor: "X", Grantee: "Y", Role: "R",
		ValidFrom: 0, ValidTo: 100, RecordedAt: 0})
	r.GrantRole(Grant{Grantor: "Y", Grantee: "X", Role: "R",
		ValidFrom: 0, ValidTo: 100, RecordedAt: 0})
	if got := r.EffectivePermissions("X", 10, 10); len(got) != 0 {
		t.Fatalf("环中无根授权，X 应无权限, 得到 %v", got)
	}
}

// 三层链 + 中间层撤回：A->B->C->D，B 撤回对 C 的委托后 D 连带失效。
func TestThreeLevelChainMiddleRevoked(t *testing.T) {
	r := NewResolver()
	r.SetRolePermissions("R", "p1")
	r.GrantRole(Grant{Grantor: "", Grantee: "A", Role: "R",
		ValidFrom: 0, ValidTo: Infinity, RecordedAt: 0})
	r.GrantRole(Grant{Grantor: "A", Grantee: "B", Role: "R",
		ValidFrom: 0, ValidTo: 100, RecordedAt: 0})
	bc := r.GrantRole(Grant{Grantor: "B", Grantee: "C", Role: "R",
		ValidFrom: 0, ValidTo: 100, RecordedAt: 0})
	r.GrantRole(Grant{Grantor: "C", Grantee: "D", Role: "R",
		ValidFrom: 0, ValidTo: 100, RecordedAt: 0})
	if !r.HoldsRole("D", "R", 50, 50) {
		t.Fatal("撤回前 D 应持有 R")
	}
	r.Revoke(bc, 60, 70)
	if r.HoldsRole("D", "R", 65, 80) {
		t.Fatal("中间层撤回后 D 在 (65, 80) 不应持有 R")
	}
	if !r.HoldsRole("D", "R", 65, 65) {
		t.Fatal("撤回未知悉前（tt=65 < 70）D 应仍持有 R")
	}
}
