package netsec

import (
	"errors"
	"reflect"
	"testing"
)

// 按键直达恢复态，恢复态镜像集合更宽，应报出最短事件序列。
func TestButtonPressReachesWiderRecovery(t *testing.T) {
	m := &Machine{
		Entry: "normal",
		States: map[string]*State{
			"normal": {
				Name:   "normal",
				Images: Digests("img-a", "img-b"),
				Edges: []Edge{
					{Event: "press-recovery-key", To: "recovery", Cond: ButtonPress},
				},
			},
			"recovery": {
				Name:   "recovery",
				Images: Digests("img-a", "img-b", "img-debug"),
			},
		},
	}
	v := m.FindWideningPath(Digests("img-a", "img-b"))
	if v == nil {
		t.Fatal("expected violation, got nil")
	}
	if v.State != "recovery" {
		t.Errorf("state = %q, want recovery", v.State)
	}
	if want := []string{"press-recovery-key"}; !reflect.DeepEqual(v.Events, want) {
		t.Errorf("events = %v, want %v", v.Events, want)
	}
	if want := []string{"img-debug"}; !reflect.DeepEqual(v.Extra, want) {
		t.Errorf("extra = %v, want %v", v.Extra, want)
	}
}

// 存在多条路径时返回最短事件序列。
func TestShortestPathWins(t *testing.T) {
	m := &Machine{
		Entry: "normal",
		States: map[string]*State{
			"normal": {
				Name:   "normal",
				Images: Digests("img-a"),
				Edges: []Edge{
					{Event: "long-1", To: "mid", Cond: ButtonPress},
					{Event: "shortcut", To: "recovery", Cond: ButtonPress},
				},
			},
			"mid": {
				Name:   "mid",
				Images: Digests("img-a"),
				Edges: []Edge{
					{Event: "long-2", To: "recovery", Cond: ButtonPress},
				},
			},
			"recovery": {
				Name:   "recovery",
				Images: Digests("img-a", "img-b"),
			},
		},
	}
	v := m.FindWideningPath(Digests("img-a"))
	if v == nil {
		t.Fatal("expected violation, got nil")
	}
	if want := []string{"shortcut"}; !reflect.DeepEqual(v.Events, want) {
		t.Errorf("events = %v, want shortest %v", v.Events, want)
	}
}

// 失败验证：唯一通往宽权限恢复态的边要求签名证明，
// 无管理授权时证明失败、边不可通过，不应报违规。
func TestSignatureGatedRecoveryNotReachable(t *testing.T) {
	m := &Machine{
		Entry: "normal",
		States: map[string]*State{
			"normal": {
				Name:   "normal",
				Images: Digests("img-a"),
				Edges: []Edge{
					{Event: "press-recovery-key", To: "verify", Cond: ButtonPress},
				},
			},
			"verify": {
				Name:   "verify",
				Images: Digests("img-a"),
				Edges: []Edge{
					{Event: "present-mgmt-signature", To: "recovery", Cond: SignatureProof},
				},
			},
			"recovery": {
				Name:   "recovery",
				Images: Digests("img-a", "img-b", "img-c"),
			},
		},
	}
	if v := m.FindWideningPath(Digests("img-a")); v != nil {
		t.Fatalf("expected no violation without management auth, got %+v", v)
	}
	if err := m.CheckRecoveryPolicy("normal"); err != nil {
		t.Fatalf("CheckRecoveryPolicy = %v, want nil", err)
	}
}

// 重启边：恢复态经重启边回到 normal 形成环，搜索必须终止；
// 且宽权限恢复态在重启前已经可达，仍应报出。
func TestRebootEdgeCycleTerminates(t *testing.T) {
	m := &Machine{
		Entry: "normal",
		States: map[string]*State{
			"normal": {
				Name:   "normal",
				Images: Digests("img-a"),
				Edges: []Edge{
					{Event: "press-recovery-key", To: "recovery", Cond: ButtonPress},
					{Event: "reboot", To: "normal", Cond: ButtonPress},
				},
			},
			"recovery": {
				Name:   "recovery",
				Images: Digests("img-a", "img-debug"),
				Edges: []Edge{
					{Event: "reboot", To: "normal", Cond: ButtonPress},
				},
			},
		},
	}
	v := m.FindWideningPath(Digests("img-a"))
	if v == nil {
		t.Fatal("expected violation, got nil")
	}
	if want := []string{"press-recovery-key"}; !reflect.DeepEqual(v.Events, want) {
		t.Errorf("events = %v, want %v", v.Events, want)
	}
}

// 只有重启自环、无宽权限状态时，搜索终止且无违规。
func TestRebootLoopOnlyNoViolation(t *testing.T) {
	m := &Machine{
		Entry: "normal",
		States: map[string]*State{
			"normal": {
				Name:   "normal",
				Images: Digests("img-a"),
				Edges: []Edge{
					{Event: "reboot", To: "normal", Cond: ButtonPress},
				},
			},
		},
	}
	if v := m.FindWideningPath(Digests("img-a")); v != nil {
		t.Fatalf("expected no violation, got %+v", v)
	}
}

// 不可达恢复入口：恢复态镜像更宽但没有任何入边，不应报违规。
func TestUnreachableRecoveryEntry(t *testing.T) {
	m := &Machine{
		Entry: "normal",
		States: map[string]*State{
			"normal": {
				Name:   "normal",
				Images: Digests("img-a"),
			},
			"recovery": {
				Name:   "recovery",
				Images: Digests("img-a", "img-b", "img-c"),
			},
		},
	}
	if v := m.FindWideningPath(Digests("img-a")); v != nil {
		t.Fatalf("expected no violation for unreachable recovery, got %+v", v)
	}
	if err := m.CheckRecoveryPolicy("normal"); err != nil {
		t.Fatalf("CheckRecoveryPolicy = %v, want nil", err)
	}
}

// 恢复态镜像集合是正常态子集（收窄）时不算扩权。
func TestNarrowerRecoveryIsAllowed(t *testing.T) {
	m := &Machine{
		Entry: "normal",
		States: map[string]*State{
			"normal": {
				Name:   "normal",
				Images: Digests("img-a", "img-b"),
				Edges: []Edge{
					{Event: "press-recovery-key", To: "recovery", Cond: ButtonPress},
				},
			},
			"recovery": {
				Name:   "recovery",
				Images: Digests("img-a"),
			},
		},
	}
	if err := m.CheckRecoveryPolicy("normal"); err != nil {
		t.Fatalf("CheckRecoveryPolicy = %v, want nil", err)
	}
}

// CheckRecoveryPolicy 返回的违规错误可断言为 *Violation。
func TestCheckRecoveryPolicyReturnsViolation(t *testing.T) {
	m := &Machine{
		Entry: "normal",
		States: map[string]*State{
			"normal": {
				Name:   "normal",
				Images: Digests("img-a"),
				Edges: []Edge{
					{Event: "press-recovery-key", To: "recovery", Cond: ButtonPress},
				},
			},
			"recovery": {
				Name:   "recovery",
				Images: Digests("img-a", "img-b"),
			},
		},
	}
	err := m.CheckRecoveryPolicy("normal")
	var v *Violation
	if !errors.As(err, &v) {
		t.Fatalf("error = %v, want *Violation", err)
	}
	if v.State != "recovery" {
		t.Errorf("state = %q, want recovery", v.State)
	}
}

// 未知状态名应报错而不是panic。
func TestUnknownState(t *testing.T) {
	m := &Machine{Entry: "normal", States: map[string]*State{}}
	if _, err := m.State("ghost"); err == nil {
		t.Fatal("expected error for unknown state")
	}
	if err := m.CheckRecoveryPolicy("ghost"); err == nil {
		t.Fatal("expected error for unknown normal state")
	}
}
