package xref_test

import (
	"fmt"

	"example.org/netsec/q0134/xref"
)

func ExampleResolver() {
	r := xref.New()

	route := xref.RouteObject{
		Group:     "gateway.networking.k8s.io",
		Kind:      "HTTPRoute",
		Namespace: "team-a",
		Name:      "checkout",
	}
	backend := xref.BackendObject{
		Kind:      "Service",
		Namespace: "team-b",
		Name:      "payments",
	}

	// 1. 没有任何授权：跨命名空间 backendRef 被拒。
	decision := r.ResolveBackend(route, backend)
	fmt.Println("before grant:", decision.Allowed, decision.Reason)

	// 2. 目标命名空间 team-b 发布 ReferenceGrant（to.name 留空即通配）。
	r.UpsertGrant(&xref.ReferenceGrant{
		Namespace: "team-b",
		Name:      "let-team-a-in",
		From: []xref.GrantFrom{{
			Group:     route.Group,
			Kind:      route.Kind,
			Namespace: route.Namespace,
		}},
		To: []xref.GrantTo{{Kind: "Service"}},
	})
	decision = r.ResolveBackend(route, backend)
	fmt.Println("after grant:", decision.Allowed, decision.Reason)

	// 3. 撤销 grant，已解析的引用立刻失效。
	r.DeleteGrant("team-b", "let-team-a-in")
	fmt.Println("after revoke, still valid:", r.IsBackendValid(decision))

	// 4. 监听器只允许带标签的命名空间挂载路由。
	listener := xref.Listener{
		Name: "web",
		AllowedRoutes: &xref.AllowedRoutes{
			Namespaces: &xref.RouteNamespaces{
				From:     xref.SelectorNamespace,
				Selector: &xref.LabelSelector{MatchLabels: map[string]string{"shared-gateway": "true"}},
			},
		},
	}
	attachment := r.ResolveAttachment("gw-ns", listener, "team-a")
	fmt.Println("attach before label:", attachment.Allowed)

	r.SetNamespaceLabels("team-a", map[string]string{"shared-gateway": "true"})
	attachment = r.ResolveAttachment("gw-ns", listener, "team-a")
	fmt.Println("attach after label:", attachment.Allowed, attachment.Reason)

	// Output:
	// before grant: false ReferenceGrantNotFound
	// after grant: true ReferenceGrantMatched
	// after revoke, still valid: false
	// attach before label: false
	// attach after label: true NamespaceSelectorMatched
}
