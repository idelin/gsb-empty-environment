package xref

import (
	"sync"
	"testing"
)

var (
	httpRoute = RouteObject{
		Group:     "gateway.networking.k8s.io",
		Kind:      "HTTPRoute",
		Namespace: "team-a",
		Name:      "web-route",
	}
	teamBService = BackendObject{
		Group:     "",
		Kind:      "Service",
		Namespace: "team-b",
		Name:      "payments",
	}
)

func grantForService(name string) *ReferenceGrant {
	return &ReferenceGrant{
		Namespace: "team-b",
		Name:      name,
		From: []GrantFrom{{
			Group:     httpRoute.Group,
			Kind:      httpRoute.Kind,
			Namespace: httpRoute.Namespace,
		}},
		To: []GrantTo{{
			Group: "",
			Kind:  "Service",
			Name:  teamBService.Name,
		}},
	}
}

// 场景 1：缺授权。目标命名空间里没有任何 ReferenceGrant 时，跨命名空间
// backendRef 必须被拒绝，而同一命名空间的引用不需要 grant。
func TestResolveBackend_MissingGrant(t *testing.T) {
	r := New()

	decision := r.ResolveBackend(httpRoute, teamBService)
	if decision.Allowed {
		t.Fatalf("cross-namespace reference without grant must be denied")
	}
	if decision.Reason != ReasonGrantNotFound {
		t.Fatalf("reason = %q, want %q", decision.Reason, ReasonGrantNotFound)
	}

	// grant 出现在错误的命名空间（路由自己的命名空间）不产生任何授权。
	wrongNS := grantForService("misplaced")
	wrongNS.Namespace = httpRoute.Namespace
	r.UpsertGrant(wrongNS)
	if d := r.ResolveBackend(httpRoute, teamBService); d.Allowed || d.Reason != ReasonGrantNotFound {
		t.Fatalf("grant outside backend namespace must not authorize, got %+v", d)
	}

	// 同命名空间引用永远放行，且不依赖 grant。
	local := teamBService
	local.Namespace = httpRoute.Namespace
	if d := r.ResolveBackend(httpRoute, local); !d.Allowed || d.Reason != ReasonSameNamespace {
		t.Fatalf("same-namespace reference must be allowed without grant, got %+v", d)
	}
}

// 场景 2：kind 不符。from 必须与路由的 group/kind/namespace 三项全等，
// to 的 kind 必须与后端相同；任一项不符都拒绝，并给出对应 reason。
func TestResolveBackend_KindMismatch(t *testing.T) {
	r := New()

	// from.kind 不符：grant 只接受 TCPRoute，引用方是 HTTPRoute。
	badFromKind := grantForService("bad-from-kind")
	badFromKind.From[0].Kind = "TCPRoute"
	r.UpsertGrant(badFromKind)
	if d := r.ResolveBackend(httpRoute, teamBService); d.Allowed || d.Reason != ReasonFromMismatch {
		t.Fatalf("from.kind mismatch must deny with %q, got %+v", ReasonFromMismatch, d)
	}

	// from.group 不符，同样落在 from 不匹配。
	badFromGroup := grantForService("bad-from-group")
	badFromGroup.From[0].Group = "wrong.example.com"
	r.UpsertGrant(badFromGroup)
	if d := r.ResolveBackend(httpRoute, teamBService); d.Allowed || d.Reason != ReasonFromMismatch {
		t.Fatalf("from.group mismatch must deny with %q, got %+v", ReasonFromMismatch, d)
	}

	// from.namespace 不符。
	badFromNS := grantForService("bad-from-ns")
	badFromNS.From[0].Namespace = "team-c"
	r.UpsertGrant(badFromNS)
	if d := r.ResolveBackend(httpRoute, teamBService); d.Allowed || d.Reason != ReasonFromMismatch {
		t.Fatalf("from.namespace mismatch must deny with %q, got %+v", ReasonFromMismatch, d)
	}

	// from 全匹配，但 to.kind 是 ConfigMap：必须以 to 不匹配拒绝。
	badToKind := grantForService("bad-to-kind")
	badToKind.To[0].Kind = "ConfigMap"
	r.UpsertGrant(badToKind)
	if d := r.ResolveBackend(httpRoute, teamBService); d.Allowed || d.Reason != ReasonToMismatch {
		t.Fatalf("to.kind mismatch must deny with %q, got %+v", ReasonToMismatch, d)
	}

	// 一个正确的 grant 加入后立即放行。
	r.UpsertGrant(grantForService("good"))
	if d := r.ResolveBackend(httpRoute, teamBService); !d.Allowed || d.Reason != ReasonGranted {
		t.Fatalf("matching grant must authorize, got %+v", d)
	}
}

// 场景 3：name 通配。to.name 为空表示该 kind 下所有对象都被授权；非空
// 时必须与后端 name 精确相等。
func TestResolveBackend_NameWildcard(t *testing.T) {
	r := New()

	// 精确 name 只授权 payments，不授权 inventory。
	r.UpsertGrant(grantForService("exact"))
	inventory := teamBService
	inventory.Name = "inventory"
	if d := r.ResolveBackend(httpRoute, inventory); d.Allowed || d.Reason != ReasonToMismatch {
		t.Fatalf("exact-name grant must not cover another backend, got %+v", d)
	}
	if d := r.ResolveBackend(httpRoute, teamBService); !d.Allowed {
		t.Fatalf("exact-name grant must cover its own backend, got %+v", d)
	}

	// 通配 grant：to.name 留空，团队 B 里任意 Service 都放行。
	wildcard := grantForService("wildcard")
	wildcard.To[0].Name = ""
	r.UpsertGrant(wildcard)
	for _, name := range []string{"payments", "inventory", "anything"} {
		backend := teamBService
		backend.Name = name
		if d := r.ResolveBackend(httpRoute, backend); !d.Allowed || d.Reason != ReasonGranted {
			t.Fatalf("wildcard grant must cover Service %q, got %+v", name, d)
		}
	}

	// 通配只对 to.kind 生效：别的 kind 仍被拒绝。
	cfgMap := BackendObject{Kind: "ConfigMap", Namespace: "team-b", Name: "settings"}
	if d := r.ResolveBackend(httpRoute, cfgMap); d.Allowed {
		t.Fatalf("wildcard Service grant must not cover ConfigMap, got %+v", d)
	}
}

// 场景 4：撤销顺序。先解析得到授权决策，再撤销 grant（删除整个 grant 或
// 改成不匹配），已解析的引用必须立刻失效，无需等待重新 list。
func TestResolveBackend_RevocationOrder(t *testing.T) {
	r := New()
	r.UpsertGrant(grantForService("grant"))

	decision := r.ResolveBackend(httpRoute, teamBService)
	if !decision.Allowed {
		t.Fatalf("expected initially allowed, got %+v", decision)
	}
	if !r.IsBackendValid(decision) {
		t.Fatalf("fresh decision must be valid")
	}
	revBefore := r.Revision()

	// 第一步：撤销（删除）grant。
	r.DeleteGrant("team-b", "grant")
	if r.Revision() == revBefore {
		t.Fatalf("revision must advance on revocation")
	}
	if r.IsBackendValid(decision) {
		t.Fatalf("cached decision must be invalid immediately after grant deletion")
	}

	// 第二步：重新解析，结论翻转为拒绝。
	denied := r.ResolveBackend(httpRoute, teamBService)
	if denied.Allowed || denied.Reason != ReasonGrantNotFound {
		t.Fatalf("re-resolution after revocation must be denied, got %+v", denied)
	}
	if !r.IsBackendValid(denied) {
		t.Fatalf("fresh denied decision must be valid")
	}

	// 第三步：以“更新”方式把 grant 改成不再匹配，旧授权同样立刻失效。
	r.UpsertGrant(grantForService("grant"))
	allowedAgain := r.ResolveBackend(httpRoute, teamBService)
	if !allowedAgain.Allowed {
		t.Fatalf("recreated grant must authorize again")
	}
	mutated := grantForService("grant")
	mutated.To[0].Name = "only-this-service"
	r.UpsertGrant(mutated)
	if r.IsBackendValid(allowedAgain) {
		t.Fatalf("decision must be invalid immediately after grant is narrowed")
	}

	// 第四步：删除整个命名空间，残留决策也必须失效。
	r.UpsertGrant(grantForService("grant"))
	stillAllowed := r.ResolveBackend(httpRoute, teamBService)
	r.DeleteNamespace("team-b")
	if r.IsBackendValid(stillAllowed) {
		t.Fatalf("namespace deletion must invalidate dependent decisions")
	}
}

// 同一份输入重复解析应命中缓存；不相关命名空间的 grant 变更不应推进
// revision，也不应影响本决策的有效性。
func TestResolveBackend_CacheAndUnrelatedChanges(t *testing.T) {
	r := New()
	r.UpsertGrant(grantForService("grant"))

	first := r.ResolveBackend(httpRoute, teamBService)
	second := r.ResolveBackend(httpRoute, teamBService)
	if first != second {
		t.Fatalf("identical resolution must return identical cached decision, got %+v vs %+v", first, second)
	}

	rev := r.Revision()
	other := grantForService("other")
	other.Namespace = "team-c"
	r.UpsertGrant(other)
	if r.Revision() == rev {
		t.Fatalf("revision must advance when a grant is added")
	}
	if !r.IsBackendValid(first) {
		t.Fatalf("grant change in another namespace must not invalidate this decision")
	}

	// 幂等更新（内容相同）不推进 revision。
	rev = r.Revision()
	r.UpsertGrant(grantForService("grant"))
	if r.Revision() != rev {
		t.Fatalf("no-op upsert must not advance revision")
	}
}

// 并发访问不触发 data race（配合 -race 运行）。
func TestResolver_Concurrent(t *testing.T) {
	r := New()
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				r.UpsertGrant(grantForService("grant"))
				d := r.ResolveBackend(httpRoute, teamBService)
				_ = r.IsBackendValid(d)
				r.DeleteGrant("team-b", "grant")
			}
		}()
	}
	wg.Wait()
}
