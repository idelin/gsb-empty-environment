package main

import (
	"reflect"
	"testing"
)

// 验收边界：A->B [10,50)，B->C [20,60)，A 在知悉 30 提前于业务 25 撤回，
// 知悉 35 查询 C 在业务 28 的权限，应判定已失效。
func TestAcceptanceScenario(t *testing.T) {
	r := NewResolver()
	r.DefineRole("R", "doc:read", "doc:write")
	r.GrantRole("A", "R", Interval{0, 100}, 0)

	ab, err := r.Delegate("A", "B", "R", Interval{10, 50}, 10)
	if err != nil {
		t.Fatalf("A->B delegation: %v", err)
	}
	if _, err := r.Delegate("B", "C", "R", Interval{20, 60}, 20); err != nil {
		t.Fatalf("B->C delegation: %v", err)
	}
	if err := r.Revoke(ab, 25, 30); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	if r.HoldsRole("C", "R", 28, 35) {
		t.Error("C must NOT hold R at business 28 as of knowledge 35 (revoked upstream)")
	}
	if got := r.EffectivePermissions("C", 28, 35); len(got) != 0 {
		t.Errorf("C permissions at (28, 35) = %v, want empty", got)
	}
}

// 双时态语义：同一业务时刻，不同知悉时间结论不同。
func TestBitemporalSemantics(t *testing.T) {
	r := NewResolver()
	r.DefineRole("R", "p1")
	r.GrantRole("A", "R", Interval{0, 100}, 0)
	ab, _ := r.Delegate("A", "B", "R", Interval{10, 50}, 10)
	r.Delegate("B", "C", "R", Interval{20, 60}, 20)
	r.Revoke(ab, 25, 30)

	cases := []struct {
		bt, kt int64
		want   bool
	}{
		{22, 35, true},  // 收回生效点 25 之前
		{28, 29, true},  // 收回在知悉 30 才进入系统，29 时尚不可见
		{28, 30, false}, // 收回已知悉且已生效
		{24, 35, true},  // 知悉 35 但业务时刻早于收回生效点
		{25, 35, false}, // 恰好在收回生效边界（半开语义）
	}
	for _, c := range cases {
		if got := r.HoldsRole("C", "R", c.bt, c.kt); got != c.want {
			t.Errorf("HoldsRole(C, R, bt=%d, kt=%d) = %v, want %v", c.bt, c.kt, got, c.want)
		}
	}
}

// 多层委托链过期：下游区间超出上游时，随上游一起失效。
func TestChainExpiryWithUpstream(t *testing.T) {
	r := NewResolver()
	r.DefineRole("R", "p1")
	r.GrantRole("A", "R", Interval{0, 100}, 0)
	r.Delegate("A", "B", "R", Interval{10, 50}, 10)
	r.Delegate("B", "C", "R", Interval{20, 60}, 20) // 区间超出上游 [10,50)

	if !r.HoldsRole("C", "R", 49, 100) {
		t.Error("C should hold R at business 49")
	}
	if r.HoldsRole("C", "R", 50, 100) {
		t.Error("C must lose R at business 50: upstream delegation A->B expired")
	}
	if r.HoldsRole("C", "R", 55, 100) {
		t.Error("C must not hold R at business 55 despite own interval [20,60)")
	}
}

// 委托前提：委托人在委托起始时刻必须实际持有角色。
func TestDelegateRequiresHeldRole(t *testing.T) {
	r := NewResolver()
	r.DefineRole("R", "p1")
	r.GrantRole("A", "R", Interval{0, 100}, 0)

	if _, err := r.Delegate("X", "Y", "R", Interval{5, 10}, 5); err == nil {
		t.Error("delegation from non-holder X must be rejected")
	}
	if _, err := r.Delegate("A", "Y", "R", Interval{100, 110}, 100); err == nil {
		t.Error("delegation starting after grant expiry must be rejected")
	}
	if _, err := r.Delegate("A", "Y", "R", Interval{50, 60}, 50); err != nil {
		t.Errorf("legitimate delegation rejected: %v", err)
	}
}

// 收回未知目标应报错；收回只影响目标记录，不误伤其他链。
func TestRevocationScope(t *testing.T) {
	r := NewResolver()
	r.DefineRole("R", "p1")
	grantA := r.GrantRole("A", "R", Interval{0, 100}, 0)
	r.GrantRole("D", "R", Interval{0, 100}, 0)
	ab, _ := r.Delegate("A", "B", "R", Interval{10, 50}, 10)
	r.Delegate("D", "E", "R", Interval{10, 50}, 10)

	if err := r.Revoke(9999, 25, 30); err == nil {
		t.Error("revoking unknown id must fail")
	}
	if err := r.Revoke(ab, 25, 30); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if r.HoldsRole("B", "R", 28, 35) {
		t.Error("B must lose R after revocation")
	}
	if !r.HoldsRole("E", "R", 28, 35) {
		t.Error("E must be unaffected by revocation of A->B")
	}
	if !r.HoldsRole("A", "R", 28, 35) {
		t.Error("A's own direct grant must be unaffected")
	}
	_ = grantA
}

// 委托环不会导致死循环，且无法凭空产生权限。
func TestDelegationCycle(t *testing.T) {
	r := NewResolver()
	r.DefineRole("R", "p1")
	r.GrantRole("A", "R", Interval{0, 100}, 0)
	r.Delegate("A", "B", "R", Interval{0, 100}, 0)
	r.Delegate("B", "A", "R", Interval{0, 100}, 0) // 成环

	if !r.HoldsRole("B", "R", 10, 10) {
		t.Error("B should hold R via A")
	}
	if r.HoldsRole("Z", "R", 10, 10) {
		t.Error("Z must not hold R")
	}
}

// 有效权限集合聚合多个角色的权限。
func TestEffectivePermissions(t *testing.T) {
	r := NewResolver()
	r.DefineRole("R1", "a", "b")
	r.DefineRole("R2", "c")
	r.GrantRole("U", "R1", Interval{0, 10}, 0)
	r.GrantRole("U", "R2", Interval{5, 15}, 0)

	if got := r.EffectivePermissions("U", 7, 0); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("perms at bt=7 = %v, want [a b c]", got)
	}
	if got := r.EffectivePermissions("U", 12, 0); !reflect.DeepEqual(got, []string{"c"}) {
		t.Errorf("perms at bt=12 = %v, want [c]", got)
	}
	if got := r.EffectivePermissions("U", 20, 0); len(got) != 0 {
		t.Errorf("perms at bt=20 = %v, want empty", got)
	}
}
