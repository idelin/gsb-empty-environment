package netsec

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

const testDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func baseTemplate() Template {
	return Template{
		Image:    ImageRef{Name: "Registry.Example.Org/Payments/API", Digest: testDigest},
		Identity: Identity{ServiceAccount: "payments-sa", SPIFFEID: "spiffe://example.org/payments"},
		Fields: map[string]any{
			"replicas": 3,
			"env":      map[string]any{"LOG_LEVEL": "info"},
			"labels":   map[string]any{"team": "pay"},
		},
	}
}

func approve(t *testing.T, tmpl Template, mutable ...string) Capability {
	t.Helper()
	cap, err := Approve(tmpl, mutable)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	return cap
}

func TestNormalizeFillsSecurityDefaults(t *testing.T) {
	norm, err := Normalize(baseTemplate())
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if norm.Image.Name != "registry.example.org/payments/api" {
		t.Fatalf("镜像名未规范化: %s", norm.Image.Name)
	}
	if !*norm.Security.RunAsNonRoot || !*norm.Security.ReadOnlyRootFilesystem ||
		*norm.Security.AllowPrivilegeEscalation || norm.Security.SeccompProfile != "RuntimeDefault" {
		t.Fatalf("安全默认值未填充: %+v", norm.Security)
	}
}

func TestApproveRejectsUnpinnedImage(t *testing.T) {
	tmpl := baseTemplate()
	tmpl.Image.Digest = ""
	if _, err := Approve(tmpl, nil); err == nil {
		t.Fatal("未锁定摘要的镜像应被拒绝")
	}
}

func TestApproveRejectsSecurityFieldInMutableSet(t *testing.T) {
	if _, err := Approve(baseTemplate(), []string{"security/runAsNonRoot"}); err == nil {
		t.Fatal("安全字段不可列入可变集合")
	}
}

func TestDeriveAllowsApprovedMutableField(t *testing.T) {
	cap := approve(t, baseTemplate(), "replicas", "env")
	inst, decisions, err := Derive(cap, []PatchOp{
		{Op: OpReplace, Path: "/replicas", Value: 5},
		{Op: OpAdd, Path: "/env/REGION", Value: "cn-north"},
	})
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if inst.Fields["replicas"] != 5 {
		t.Fatalf("replicas 未更新: %v", inst.Fields["replicas"])
	}
	for _, d := range decisions {
		if !d.Allowed || d.Reason != ReasonApprovedMutable || d.Basis == "" {
			t.Fatalf("授权依据缺失: %+v", d)
		}
	}
}

func TestDeriveDeniesSecurityAndUnlistedFields(t *testing.T) {
	cap := approve(t, baseTemplate(), "replicas")
	_, decisions, err := Derive(cap, []PatchOp{
		{Op: OpReplace, Path: "/identity/serviceAccount", Value: "root-sa"},
		{Op: OpReplace, Path: "/image/digest", Value: "sha256:deadbeef"},
		{Op: OpReplace, Path: "/replicas", Value: 9},
		{Op: OpAdd, Path: "/hostNetwork", Value: true},
	})
	if !errors.Is(err, ErrUnauthorizedChange) {
		t.Fatalf("应整体失败: %v", err)
	}
	if len(decisions) != 4 {
		t.Fatalf("应返回每项判定: %d", len(decisions))
	}
	if decisions[0].Reason != ReasonSecurityBound || decisions[1].Reason != ReasonSecurityBound {
		t.Fatalf("安全字段应被拒绝: %+v", decisions[:2])
	}
	if !decisions[2].Allowed {
		t.Fatalf("批准字段应放行: %+v", decisions[2])
	}
	if decisions[3].Reason != ReasonNotMutable {
		t.Fatalf("未列入集合的非安全字段应被拒绝: %+v", decisions[3])
	}
}

func TestDeriveMoveField(t *testing.T) {
	cap := approve(t, baseTemplate(), "env", "labels")
	inst, decisions, err := Derive(cap, []PatchOp{
		{Op: OpMove, From: "/env/LOG_LEVEL", Path: "/labels/log-level"},
	})
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	env := inst.Fields["env"].(map[string]any)
	if _, ok := env["LOG_LEVEL"]; ok {
		t.Fatal("源字段应被移除")
	}
	if inst.Fields["labels"].(map[string]any)["log-level"] != "info" {
		t.Fatal("目标字段应携带移动值")
	}
	if decisions[0].Reason != ReasonMoveAuthorized {
		t.Fatalf("移动依据: %+v", decisions[0])
	}
}

func TestDeriveMoveIntoSecurityFieldDenied(t *testing.T) {
	cap := approve(t, baseTemplate(), "env")
	_, decisions, err := Derive(cap, []PatchOp{
		{Op: OpMove, From: "/env/LOG_LEVEL", Path: "/security/runAsNonRoot"},
	})
	if !errors.Is(err, ErrUnauthorizedChange) || decisions[0].Reason != ReasonSecurityBound {
		t.Fatalf("移入安全字段应被拒绝: %v %+v", err, decisions[0])
	}
}

func TestDeriveRemoveRestoresApprovedDefault(t *testing.T) {
	cap := approve(t, baseTemplate(), "env")
	inst, decisions, err := Derive(cap, []PatchOp{
		{Op: OpReplace, Path: "/env/LOG_LEVEL", Value: "debug"},
		{Op: OpRemove, Path: "/env/LOG_LEVEL"},
	})
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	got := inst.Fields["env"].(map[string]any)["LOG_LEVEL"]
	if got != "info" {
		t.Fatalf("删除后应恢复批准默认值 info,得到 %v", got)
	}
	last := decisions[len(decisions)-1]
	if last.Reason != ReasonDefaultRestored || !strings.Contains(last.Basis, "默认值") {
		t.Fatalf("默认值恢复依据缺失: %+v", last)
	}
}

func TestConcurrentTemplateUpdates(t *testing.T) {
	store, err := NewStore(baseTemplate())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	const workers = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins, conflicts := 0, 0
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, decisions, err := store.Update(1, []PatchOp{
				{Op: OpReplace, Path: "/replicas", Value: n},
			})
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				wins++
			} else if errors.Is(err, ErrRevisionConflict) {
				conflicts++
				if decisions[0].Reason != ReasonRevisionConflict {
					t.Errorf("冲突依据缺失: %+v", decisions[0])
				}
			} else {
				t.Errorf("意外错误: %v", err)
			}
		}(i + 10)
	}
	wg.Wait()
	if wins != 1 || conflicts != workers-1 {
		t.Fatalf("应恰有一个更新成功: wins=%d conflicts=%d", wins, conflicts)
	}
	if store.Current().Revision != 2 {
		t.Fatalf("版本应递增到 2: %d", store.Current().Revision)
	}
}

func TestTemplateUpdateSecurityFieldRequiresReapproval(t *testing.T) {
	store, _ := NewStore(baseTemplate())
	_, decisions, err := store.Update(1, []PatchOp{
		{Op: OpReplace, Path: "/security/runAsNonRoot", Value: false},
	})
	if !errors.Is(err, ErrUnauthorizedChange) || decisions[0].Reason != ReasonRequiresReapprove {
		t.Fatalf("安全字段更新应要求重新审批: %v %+v", err, decisions[0])
	}
}

func TestStaleCapabilityRejectedAfterTemplateUpdate(t *testing.T) {
	store, _ := NewStore(baseTemplate())
	cap, err := store.Approve([]string{"replicas"})
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if _, _, err := store.Update(1, []PatchOp{{Op: OpReplace, Path: "/replicas", Value: 4}}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	_, decisions, err := store.Derive(cap, []PatchOp{{Op: OpReplace, Path: "/replicas", Value: 7}})
	if !errors.Is(err, ErrStaleCapability) {
		t.Fatalf("审批后模板变更应使能力失效: %v", err)
	}
	if decisions[0].Reason != ReasonRequiresReapprove {
		t.Fatalf("失效依据缺失: %+v", decisions[0])
	}
}

func TestDerivedInstanceInheritsApprovedSecurity(t *testing.T) {
	cap := approve(t, baseTemplate(), "replicas")
	inst, _, err := Derive(cap, []PatchOp{{Op: OpReplace, Path: "/replicas", Value: 2}})
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if inst.Image.Digest != testDigest || inst.Identity.ServiceAccount != "payments-sa" ||
		!*inst.Security.RunAsNonRoot {
		t.Fatalf("实例应原样继承批准的安全绑定: %+v", inst)
	}
}
