package xref

import "testing"

func selectorListener(name string, matchLabels map[string]string) Listener {
	return Listener{
		Name: name,
		AllowedRoutes: &AllowedRoutes{
			Namespaces: &RouteNamespaces{
				From:     SelectorNamespace,
				Selector: &LabelSelector{MatchLabels: matchLabels},
			},
		},
	}
}

func modeListener(name string, mode FromNamespaces) Listener {
	return Listener{
		Name: name,
		AllowedRoutes: &AllowedRoutes{
			Namespaces: &RouteNamespaces{From: mode},
		},
	}
}

// Same（默认）：只允许网关所在命名空间的路由。
func TestResolveAttachment_SameNamespace(t *testing.T) {
	r := New()

	for _, listener := range []Listener{
		{Name: "nil-block"},
		{Name: "nil-namespaces", AllowedRoutes: &AllowedRoutes{}},
		modeListener("explicit-same", SameNamespace),
	} {
		if d := r.ResolveAttachment("gw-ns", listener, "gw-ns"); !d.Allowed || d.Reason != ReasonSameNamespaceMode {
			t.Fatalf("%s: local route must attach, got %+v", listener.Name, d)
		}
		if d := r.ResolveAttachment("gw-ns", listener, "team-a"); d.Allowed || d.Reason != ReasonSameNamespaceDeny {
			t.Fatalf("%s: foreign route must be denied, got %+v", listener.Name, d)
		}
	}
}

// All：任意命名空间的路由都可以挂载。
func TestResolveAttachment_AllNamespaces(t *testing.T) {
	r := New()
	listener := modeListener("open", AllNamespaces)

	for _, routeNS := range []string{"gw-ns", "team-a", "team-b"} {
		if d := r.ResolveAttachment("gw-ns", listener, routeNS); !d.Allowed || d.Reason != ReasonAllNamespacesMode {
			t.Fatalf("All mode must allow namespace %q, got %+v", routeNS, d)
		}
	}
}

// Selector：按路由命名空间标签判定；标签变化（撤销）后已解析的挂载
// 立刻失效。
func TestResolveAttachment_SelectorAndLabelRevocation(t *testing.T) {
	r := New()
	listener := selectorListener("labeled", map[string]string{
		"shared-gateway": "true",
		"tier":           "frontend",
	})

	// 命名空间没有标签：拒绝。
	denied := r.ResolveAttachment("gw-ns", listener, "team-a")
	if denied.Allowed || denied.Reason != ReasonSelectorNoMatch {
		t.Fatalf("unlabeled namespace must not match selector, got %+v", denied)
	}

	// 只满足部分标签：仍拒绝。
	r.SetNamespaceLabels("team-a", map[string]string{"shared-gateway": "true"})
	if d := r.ResolveAttachment("gw-ns", listener, "team-a"); d.Allowed || d.Reason != ReasonSelectorNoMatch {
		t.Fatalf("partial label match must be denied, got %+v", d)
	}

	// 全部满足：放行。
	r.SetNamespaceLabels("team-a", map[string]string{
		"shared-gateway": "true",
		"tier":           "frontend",
	})
	allowed := r.ResolveAttachment("gw-ns", listener, "team-a")
	if !allowed.Allowed || allowed.Reason != ReasonSelectorMatched {
		t.Fatalf("matching labels must allow attachment, got %+v", allowed)
	}
	if !r.IsAttachmentValid(allowed) {
		t.Fatalf("fresh selector decision must be valid")
	}

	// 撤销标签：已解析的挂载立刻失效，重新解析翻转为拒绝。
	r.SetNamespaceLabels("team-a", map[string]string{"shared-gateway": "true"})
	if r.IsAttachmentValid(allowed) {
		t.Fatalf("label revocation must invalidate selector decision immediately")
	}
	if d := r.ResolveAttachment("gw-ns", listener, "team-a"); d.Allowed {
		t.Fatalf("re-resolution after label removal must be denied, got %+v", d)
	}

	// 删除命名空间同样使旧决策失效。
	r.SetNamespaceLabels("team-a", map[string]string{
		"shared-gateway": "true",
		"tier":           "frontend",
	})
	allowedAgain := r.ResolveAttachment("gw-ns", listener, "team-a")
	r.DeleteNamespace("team-a")
	if r.IsAttachmentValid(allowedAgain) {
		t.Fatalf("namespace deletion must invalidate selector decision")
	}

	// All/Same 模式不读取标签，标签变化不应使其失效。
	allDecision := r.ResolveAttachment("gw-ns", modeListener("open", AllNamespaces), "team-a")
	r.SetNamespaceLabels("team-a", map[string]string{"anything": "else"})
	if !r.IsAttachmentValid(allDecision) {
		t.Fatalf("All-mode decision must not depend on namespace labels")
	}
}

// Selector 模式却没有提供 selector 时按失败关闭处理。
func TestResolveAttachment_SelectorWithoutSelector(t *testing.T) {
	r := New()
	listener := modeListener("broken", SelectorNamespace)
	r.SetNamespaceLabels("team-a", map[string]string{"shared-gateway": "true"})

	if d := r.ResolveAttachment("gw-ns", listener, "team-a"); d.Allowed || d.Reason != ReasonSelectorMissing {
		t.Fatalf("Selector mode without selector must fail closed, got %+v", d)
	}
}

// 决策保存了 listener 配置的快照，调用方之后修改原 listener 不影响决策。
func TestResolveAttachment_ListenerConfigSnapshot(t *testing.T) {
	r := New()
	listener := modeListener("open", AllNamespaces)
	decision := r.ResolveAttachment("gw-ns", listener, "team-a")

	listener.AllowedRoutes.Namespaces.From = SameNamespace
	if !r.IsAttachmentValid(decision) {
		t.Fatalf("mutating caller-owned listener must not change the captured decision")
	}
}
