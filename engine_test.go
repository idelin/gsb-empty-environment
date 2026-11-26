package networkpolicy

import (
	"net/netip"
	"strings"
	"testing"
)

func testCluster(t *testing.T) *Cluster {
	t.Helper()
	cluster := NewCluster()
	for _, namespace := range []Namespace{
		{Name: "default", Labels: map[string]string{"env": "test", "team": "core"}},
		{Name: "team-a", Labels: map[string]string{"env": "prod", "team": "a"}},
		{Name: "team-b", Labels: map[string]string{"env": "prod", "team": "b"}},
	} {
		if err := cluster.AddNamespace(namespace); err != nil {
			t.Fatalf("add namespace: %v", err)
		}
	}
	for _, pod := range []Pod{
		{Name: "a", Namespace: "default", Labels: map[string]string{"app": "a"}, IP: "10.0.0.1"},
		{Name: "b", Namespace: "default", Labels: map[string]string{"app": "b"}, IP: "10.0.0.2"},
		{Name: "c", Namespace: "default", Labels: map[string]string{"app": "c"}, IP: "10.0.0.3"},
		{Name: "target", Namespace: "default", Labels: map[string]string{"app": "target"}, IP: "10.0.0.10"},
	} {
		if err := cluster.AddPod(pod); err != nil {
			t.Fatalf("add pod: %v", err)
		}
	}
	return cluster
}

func mustCompile(t *testing.T, cluster *Cluster, policies ...NetworkPolicy) *Engine {
	t.Helper()
	engine, err := Compile(cluster, policies)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return engine
}

func requireEvaluate(t *testing.T, engine *Engine, src, dst any, protocol any, port any, want bool) {
	t.Helper()
	got, err := engine.Evaluate(src, dst, protocol, port)
	if err != nil {
		t.Fatalf("evaluate %v -> %v: %v", src, dst, err)
	}
	if got != want {
		t.Errorf("evaluate %v -> %v %v/%v = %v, want %v", src, dst, protocol, port, got, want)
	}
}

func TestUnselectedPodsAreDefaultAllow(t *testing.T) {
	cluster := testCluster(t)
	engine := mustCompile(t, cluster)

	requireEvaluate(t, engine, "default/a", "default/b", TCP, 80, true)
	requireEvaluate(t, engine, "default/b", "default/a", TCP, 80, true)
	requireEvaluate(t, engine, "default/a", "10.0.0.99", UDP, 53, true)
	requireEvaluate(t, engine, "10.0.0.99", "default/a", SCTP, 9000, true)
}

func TestIngressIsolationDoesNotIsolateEgress(t *testing.T) {
	cluster := testCluster(t)
	engine := mustCompile(t, cluster, NetworkPolicy{
		Name:        "allow-b-to-a",
		Namespace:   "default",
		PodSelector: LabelSelector{"app": "a"},
		PolicyTypes: []PolicyType{Ingress},
		Ingress: []IngressRule{{
			From: []Peer{{PodSelector: LabelSelector{"app": "b"}}},
		}},
	})

	requireEvaluate(t, engine, "default/b", "default/a", TCP, 80, true)
	requireEvaluate(t, engine, "default/c", "default/a", TCP, 80, false)
	// Known Pod IPs retain the Pod's policy identity.
	requireEvaluate(t, engine, "default/b", "10.0.0.1", TCP, 80, true)
	requireEvaluate(t, engine, "10.0.0.3", "default/a", TCP, 80, false)
	// a has no Egress isolation, so its own outbound traffic is unaffected.
	requireEvaluate(t, engine, "default/a", "default/c", TCP, 80, true)
	requireEvaluate(t, engine, "default/a", "10.0.0.99", TCP, 80, true)

	isolatedIngress, err := engine.IsIsolated("default/a", Ingress)
	if err != nil || !isolatedIngress {
		t.Fatalf("a should be ingress-isolated, got %v, %v", isolatedIngress, err)
	}
	isolatedEgress, err := engine.IsIsolated("default/a", Egress)
	if err != nil || isolatedEgress {
		t.Fatalf("a should not be egress-isolated, got %v, %v", isolatedEgress, err)
	}
}

func TestEmptyIngressListVersusEmptyRule(t *testing.T) {
	cluster := testCluster(t)

	denyEngine := mustCompile(t, cluster, NetworkPolicy{
		Name:        "deny-all",
		Namespace:   "default",
		PodSelector: LabelSelector{"app": "a"},
		PolicyTypes: []PolicyType{Ingress},
		Ingress:     []IngressRule{},
	})
	requireEvaluate(t, denyEngine, "default/b", "default/a", TCP, 80, false)

	allowEngine := mustCompile(t, cluster, NetworkPolicy{
		Name:        "allow-all",
		Namespace:   "default",
		PodSelector: LabelSelector{"app": "b"},
		PolicyTypes: []PolicyType{Ingress},
		Ingress:     []IngressRule{{}},
	})
	requireEvaluate(t, allowEngine, "default/c", "default/b", TCP, 80, true)
	requireEvaluate(t, allowEngine, "10.0.0.99", "default/b", TCP, 80, true)
}

func TestPeerSelectorsANDVersusPeersOR(t *testing.T) {
	cluster := testCluster(t)
	for _, pod := range []Pod{
		{Name: "prod-db-a", Namespace: "team-a", Labels: map[string]string{"role": "db"}, IP: "10.1.0.1"},
		{Name: "prod-db-b", Namespace: "team-b", Labels: map[string]string{"role": "db"}, IP: "10.2.0.1"},
		{Name: "prod-web-a", Namespace: "team-a", Labels: map[string]string{"role": "web"}, IP: "10.1.0.2"},
		{Name: "local-db", Namespace: "default", Labels: map[string]string{"role": "db"}, IP: "10.0.0.20"},
		{Name: "local-web", Namespace: "default", Labels: map[string]string{"role": "web"}, IP: "10.0.0.21"},
	} {
		if err := cluster.AddPod(pod); err != nil {
			t.Fatalf("add pod: %v", err)
		}
	}

	andEngine := mustCompile(t, cluster, NetworkPolicy{
		Name:        "and",
		Namespace:   "default",
		PodSelector: LabelSelector{"app": "target"},
		PolicyTypes: []PolicyType{Ingress},
		Ingress: []IngressRule{{
			From: []Peer{{
				NamespaceSelector: LabelSelector{"env": "prod"},
				PodSelector:       LabelSelector{"role": "db"},
			}},
		}},
	})
	// Both selectors in one peer are ANDed.
	requireEvaluate(t, andEngine, "team-a/prod-db-a", "default/target", TCP, 80, true)
	requireEvaluate(t, andEngine, "team-b/prod-db-b", "default/target", TCP, 80, true)
	requireEvaluate(t, andEngine, "team-a/prod-web-a", "default/target", TCP, 80, false)
	requireEvaluate(t, andEngine, "default/local-db", "default/target", TCP, 80, false)

	orEngine := mustCompile(t, cluster, NetworkPolicy{
		Name:        "or",
		Namespace:   "default",
		PodSelector: LabelSelector{"app": "target"},
		PolicyTypes: []PolicyType{Ingress},
		Ingress: []IngressRule{{
			From: []Peer{
				{NamespaceSelector: LabelSelector{"env": "prod"}},
				{PodSelector: LabelSelector{"role": "db"}},
			},
		}},
	})
	// Separate peers are ORed. The second peer has no namespaceSelector, so it
	// is limited to the policy's namespace.
	requireEvaluate(t, orEngine, "team-a/prod-web-a", "default/target", TCP, 80, true)
	requireEvaluate(t, orEngine, "default/local-db", "default/target", TCP, 80, true)
	requireEvaluate(t, orEngine, "team-a/prod-db-a", "default/target", TCP, 80, true)
	requireEvaluate(t, orEngine, "default/local-web", "default/target", TCP, 80, false)
}

func TestOmittedNamespaceSelectorUsesPolicyNamespace(t *testing.T) {
	cluster := testCluster(t)
	if err := cluster.AddPod(Pod{
		Name: "other-db", Namespace: "team-a", Labels: map[string]string{"role": "db"}, IP: "10.1.0.1",
	}); err != nil {
		t.Fatalf("add pod: %v", err)
	}
	if err := cluster.AddPod(Pod{
		Name: "same-db", Namespace: "default", Labels: map[string]string{"role": "db"}, IP: "10.0.0.30",
	}); err != nil {
		t.Fatalf("add pod: %v", err)
	}
	engine := mustCompile(t, cluster, NetworkPolicy{
		Name:        "local-only",
		Namespace:   "default",
		PodSelector: LabelSelector{"app": "target"},
		PolicyTypes: []PolicyType{Ingress},
		Ingress: []IngressRule{{
			From: []Peer{{PodSelector: LabelSelector{"role": "db"}}},
		}},
	})
	requireEvaluate(t, engine, "default/same-db", "default/target", TCP, 80, true)
	requireEvaluate(t, engine, "team-a/other-db", "default/target", TCP, 80, false)
}

func TestIPBlockOverlappingExceptDifference(t *testing.T) {
	cluster := testCluster(t)
	prefixes, err := PrefixDifference("10.0.0.0/8", []string{
		"10.1.0.0/16",
		"10.1.128.0/17",
		"10.2.0.0/16",
	})
	if err != nil {
		t.Fatalf("prefix difference: %v", err)
	}
	for i := range prefixes {
		for j := i + 1; j < len(prefixes); j++ {
			if prefixesOverlap(prefixes[i], prefixes[j]) {
				t.Fatalf("result prefixes overlap: %v and %v", prefixes[i], prefixes[j])
			}
		}
	}
	block := IPBlock{CIDR: "10.0.0.0/8", Except: []string{"10.1.0.0/16", "10.1.128.0/17", "10.2.0.0/16"}}
	for _, raw := range []string{
		"10.0.0.1", "10.0.255.255", "10.3.0.1", "10.255.255.255",
	} {
		addr := netip.MustParseAddr(raw)
		if !block.Allows(addr) {
			t.Errorf("%s should be allowed", raw)
		}
	}
	for _, raw := range []string{
		"10.1.0.1", "10.1.127.255", "10.1.128.1", "10.1.255.255", "10.2.0.1", "10.2.255.255", "192.168.0.1",
	} {
		addr := netip.MustParseAddr(raw)
		if block.Allows(addr) {
			t.Errorf("%s should be excluded", raw)
		}
	}

	engine := mustCompile(t, cluster, NetworkPolicy{
		Name:        "ipblock",
		Namespace:   "default",
		PodSelector: LabelSelector{"app": "target"},
		PolicyTypes: []PolicyType{Ingress},
		Ingress: []IngressRule{{
			From: []Peer{{IPBlock: &IPBlock{
				CIDR:   "10.0.0.0/8",
				Except: []string{"10.1.0.0/16", "10.1.128.0/17", "10.2.0.0/16"},
			}}},
		}},
	})
	requireEvaluate(t, engine, "10.0.0.1", "default/target", TCP, 80, true)
	requireEvaluate(t, engine, "10.1.200.1", "default/target", TCP, 80, false)
	requireEvaluate(t, engine, "10.2.1.1", "default/target", TCP, 80, false)
	requireEvaluate(t, engine, "192.168.0.1", "default/target", TCP, 80, false)

	reverseNested := IPBlock{CIDR: "10.0.0.0/8", Except: []string{"10.0.64.0/18", "10.0.0.0/16"}}
	if !reverseNested.Allows(netip.MustParseAddr("10.1.0.1")) {
		t.Error("10.1.0.1 should remain after removing a child and then its parent")
	}
	if reverseNested.Allows(netip.MustParseAddr("10.0.0.1")) ||
		reverseNested.Allows(netip.MustParseAddr("10.0.100.1")) {
		t.Error("the parent /16 exception should remove all of 10.0.0.0/16")
	}
	if prefixes, err := PrefixDifference("10.0.0.0/8", []string{"10.0.0.0/8"}); err != nil || len(prefixes) != 0 {
		t.Errorf("an exception equal to the CIDR should leave no prefixes: %v, %v", prefixes, err)
	}

	for _, invalid := range []IPBlock{
		{CIDR: "10.0.0.0/8", Except: []string{"172.16.0.0/12"}},
		{CIDR: "10.0.0.0/8", Except: []string{"fd00::/8"}},
		{CIDR: "not-a-cidr"},
		{CIDR: "10.0.0.0/8", Except: []string{"nonsense"}},
	} {
		if _, err := Compile(cluster, []NetworkPolicy{{
			Namespace:   "default",
			PodSelector: LabelSelector{"app": "target"},
			Ingress: []IngressRule{{
				From: []Peer{{IPBlock: &invalid}},
			}},
		}}); err == nil {
			t.Errorf("invalid ipBlock %+v should fail compilation", invalid)
		}
	}
}

func TestIPv6IPBlockAndDualStack(t *testing.T) {
	cluster := testCluster(t)
	if err := cluster.AddPod(Pod{
		Name:      "v6target",
		Namespace: "default",
		Labels:    map[string]string{"app": "v6target"},
		IPs:       []string{"fd00:0:1::1", "10.0.0.40"},
	}); err != nil {
		t.Fatalf("add pod: %v", err)
	}

	block := IPBlock{CIDR: "fd00::/8", Except: []string{"fd00:1::/32", "fd00:1:8000::/33"}}
	prefixes, err := block.Prefixes()
	if err != nil {
		t.Fatalf("prefixes: %v", err)
	}
	if len(prefixes) == 0 {
		t.Fatal("expected remaining IPv6 prefixes")
	}
	engine := mustCompile(t, cluster, NetworkPolicy{
		Name:        "v6",
		Namespace:   "default",
		PodSelector: LabelSelector{"app": "v6target"},
		PolicyTypes: []PolicyType{Ingress},
		Ingress: []IngressRule{{
			From: []Peer{{IPBlock: &block}},
		}},
	})
	requireEvaluate(t, engine, "fd00:0:1::2", "default/v6target", TCP, 80, true)
	requireEvaluate(t, engine, "fd00:1::2", "default/v6target", TCP, 80, false)
	requireEvaluate(t, engine, "fd00:1:9000::2", "default/v6target", TCP, 80, false)
	requireEvaluate(t, engine, "10.0.0.50", "default/v6target", TCP, 80, false)
}

func TestNamedPortsResolvePerDestinationPod(t *testing.T) {
	cluster := testCluster(t)
	for _, pod := range []Pod{
		{
			Name: "web-a", Namespace: "default", Labels: map[string]string{"tier": "web"}, IP: "10.0.0.50",
			Ports: map[string]ContainerPort{"http": {ContainerPort: 8080, Protocol: TCP}},
		},
		{
			Name: "web-b", Namespace: "default", Labels: map[string]string{"tier": "web"}, IP: "10.0.0.51",
			Ports: map[string]ContainerPort{"http": {ContainerPort: 9090, Protocol: TCP}},
		},
	} {
		if err := cluster.AddPod(pod); err != nil {
			t.Fatalf("add pod: %v", err)
		}
	}
	engine := mustCompile(t, cluster, NetworkPolicy{
		Name:        "named-http",
		Namespace:   "default",
		PodSelector: LabelSelector{"tier": "web"},
		PolicyTypes: []PolicyType{Ingress},
		Ingress: []IngressRule{{
			Ports: []NetworkPolicyPort{{Name: "http"}},
		}},
	})

	requireEvaluate(t, engine, "default/a", "default/web-a", TCP, 8080, true)
	requireEvaluate(t, engine, "default/a", "default/web-a", TCP, 9090, false)
	requireEvaluate(t, engine, "default/a", "default/web-b", TCP, 9090, true)
	requireEvaluate(t, engine, "default/a", "default/web-b", TCP, 8080, false)

	// The same named destination port can also be supplied directly.
	requireEvaluate(t, engine, "default/a", "default/web-a", TCP, "http", true)
	requireEvaluate(t, engine, "default/a", "default/web-b", TCP, "http", true)
}

func TestNamedEgressPortResolvesPerSourcePod(t *testing.T) {
	cluster := testCluster(t)
	if err := cluster.AddPod(Pod{
		Name: "client", Namespace: "default", Labels: map[string]string{"app": "client"}, IP: "10.0.0.60",
		Ports: map[string]ContainerPort{"proxy": {ContainerPort: 3128, Protocol: TCP}},
	}); err != nil {
		t.Fatalf("add pod: %v", err)
	}
	engine := mustCompile(t, cluster, NetworkPolicy{
		Name:        "egress-named",
		Namespace:   "default",
		PodSelector: LabelSelector{"app": "client"},
		PolicyTypes: []PolicyType{Egress},
		Egress: []EgressRule{{
			Ports: []NetworkPolicyPort{{Name: "proxy"}},
		}},
	})
	requireEvaluate(t, engine, "default/client", "default/b", TCP, 3128, true)
	requireEvaluate(t, engine, "default/client", "default/b", TCP, 3129, false)
}

func TestEndPortIsInclusiveAndNumericOnly(t *testing.T) {
	cluster := testCluster(t)
	engine := mustCompile(t, cluster, NetworkPolicy{
		Name:        "range",
		Namespace:   "default",
		PodSelector: LabelSelector{"app": "target"},
		PolicyTypes: []PolicyType{Ingress},
		Ingress: []IngressRule{{
			Ports: []NetworkPolicyPort{{Port: 8000, EndPort: 8080}},
		}},
	})
	requireEvaluate(t, engine, "default/a", "default/target", TCP, 8000, true)
	requireEvaluate(t, engine, "default/a", "default/target", TCP, 8080, true)
	requireEvaluate(t, engine, "default/a", "default/target", TCP, 7999, false)
	requireEvaluate(t, engine, "default/a", "default/target", TCP, 8081, false)
	requireEvaluate(t, engine, "default/a", "default/target", UDP, 8000, false)

	for _, port := range []NetworkPolicyPort{
		{Name: "http", EndPort: 8080},
		{Port: 8000, EndPort: 7999},
		{Port: 70000},
		{Port: 80, Protocol: "ICMP"},
	} {
		if _, err := Compile(cluster, []NetworkPolicy{{
			Namespace:   "default",
			PodSelector: LabelSelector{"app": "target"},
			Ingress: []IngressRule{{
				Ports: []NetworkPolicyPort{port},
			}},
		}}); err == nil {
			t.Errorf("invalid port %+v should fail compilation", port)
		}
	}
}

func TestPortsAndPeersAreANDed(t *testing.T) {
	cluster := testCluster(t)
	engine := mustCompile(t, cluster, NetworkPolicy{
		Name:        "peer-and-port",
		Namespace:   "default",
		PodSelector: LabelSelector{"app": "target"},
		PolicyTypes: []PolicyType{Ingress},
		Ingress: []IngressRule{{
			From:  []Peer{{PodSelector: LabelSelector{"app": "a"}}},
			Ports: []NetworkPolicyPort{{Port: 80}},
		}},
	})
	requireEvaluate(t, engine, "default/a", "default/target", TCP, 80, true)
	requireEvaluate(t, engine, "default/a", "default/target", TCP, 81, false)
	requireEvaluate(t, engine, "default/b", "default/target", TCP, 80, false)
}

func TestSourceEgressAndDestinationIngressMustBothAllow(t *testing.T) {
	cluster := testCluster(t)
	bothOpen := []NetworkPolicy{
		{
			Name:        "egress-open",
			Namespace:   "default",
			PodSelector: LabelSelector{"app": "a"},
			PolicyTypes: []PolicyType{Egress},
			Egress:      []EgressRule{{}},
		},
		{
			Name:        "ingress-open",
			Namespace:   "default",
			PodSelector: LabelSelector{"app": "b"},
			PolicyTypes: []PolicyType{Ingress},
			Ingress:     []IngressRule{{}},
		},
	}
	engine := mustCompile(t, cluster, bothOpen...)
	requireEvaluate(t, engine, "default/a", "default/b", TCP, 80, true)

	ingressClosed := []NetworkPolicy{
		bothOpen[0],
		{
			Name:        "ingress-closed",
			Namespace:   "default",
			PodSelector: LabelSelector{"app": "b"},
			PolicyTypes: []PolicyType{Ingress},
			Ingress:     []IngressRule{},
		},
	}
	engine = mustCompile(t, cluster, ingressClosed...)
	egressAllowed, err := engine.AllowsEgress("default/a", "default/b", TCP, 80)
	if err != nil {
		t.Fatalf("allows egress: %v", err)
	}
	ingressAllowed, err := engine.AllowsIngress("default/a", "default/b", TCP, 80)
	if err != nil {
		t.Fatalf("allows ingress: %v", err)
	}
	if !egressAllowed || ingressAllowed {
		t.Fatalf("expected egress=true ingress=false, got %v/%v", egressAllowed, ingressAllowed)
	}
	requireEvaluate(t, engine, "default/a", "default/b", TCP, 80, false)

	egressClosed := []NetworkPolicy{
		{
			Name:        "egress-closed",
			Namespace:   "default",
			PodSelector: LabelSelector{"app": "a"},
			PolicyTypes: []PolicyType{Egress},
			Egress:      []EgressRule{},
		},
		bothOpen[1],
	}
	engine = mustCompile(t, cluster, egressClosed...)
	requireEvaluate(t, engine, "default/a", "default/b", TCP, 80, false)

	// A policy-selected source and a policy-selected destination can also be
	// joined by per-rule peer and port checks.
	engine = mustCompile(t, cluster,
		NetworkPolicy{
			Name:        "egress-to-b",
			Namespace:   "default",
			PodSelector: LabelSelector{"app": "a"},
			PolicyTypes: []PolicyType{Egress},
			Egress: []EgressRule{{
				To:    []Peer{{PodSelector: LabelSelector{"app": "b"}}},
				Ports: []NetworkPolicyPort{{Port: 80}},
			}},
		},
		NetworkPolicy{
			Name:        "ingress-from-a",
			Namespace:   "default",
			PodSelector: LabelSelector{"app": "b"},
			PolicyTypes: []PolicyType{Ingress},
			Ingress: []IngressRule{{
				From:  []Peer{{PodSelector: LabelSelector{"app": "a"}}},
				Ports: []NetworkPolicyPort{{Port: 80}},
			}},
		},
	)
	requireEvaluate(t, engine, "default/a", "default/b", TCP, 80, true)
	requireEvaluate(t, engine, "default/a", "default/b", TCP, 81, false)
	requireEvaluate(t, engine, "default/c", "default/b", TCP, 80, false)
	requireEvaluate(t, engine, "default/a", "default/c", TCP, 80, false)
}

func TestPolicyTypeInference(t *testing.T) {
	cluster := testCluster(t)

	types, err := EffectivePolicyTypes(NetworkPolicy{Egress: []EgressRule{}})
	if err != nil {
		t.Fatalf("effective policy types: %v", err)
	}
	if len(types) != 2 || types[0] != Ingress || types[1] != Egress {
		t.Fatalf("presence of egress field should imply Ingress+Egress, got %v", types)
	}
	types, err = EffectivePolicyTypes(NetworkPolicy{})
	if err != nil {
		t.Fatalf("effective policy types: %v", err)
	}
	if len(types) != 1 || types[0] != Ingress {
		t.Fatalf("an omitted egress field should imply Ingress only, got %v", types)
	}

	ingressOnly := mustCompile(t, cluster, NetworkPolicy{
		Name:        "implicit-ingress",
		Namespace:   "default",
		PodSelector: LabelSelector{"app": "a"},
		Ingress:     []IngressRule{{}},
	})
	for _, direction := range []PolicyType{Ingress, Egress} {
		got, err := ingressOnly.IsIsolated("default/a", direction)
		if err != nil {
			t.Fatalf("isolated: %v", err)
		}
		if direction == Ingress && !got {
			t.Errorf("implicit policy should isolate ingress")
		}
		if direction == Egress && got {
			t.Errorf("implicit policy without egress field must not isolate egress")
		}
	}

	explicitEgressField := mustCompile(t, cluster, NetworkPolicy{
		Name:        "implicit-both",
		Namespace:   "default",
		PodSelector: LabelSelector{"app": "b"},
		Egress:      []EgressRule{},
	})
	for _, direction := range []PolicyType{Ingress, Egress} {
		got, err := explicitEgressField.IsIsolated("default/b", direction)
		if err != nil || !got {
			t.Errorf("presence of egress field should imply %s isolation, got %v, %v", direction, got, err)
		}
	}
	requireEvaluate(t, explicitEgressField, "default/b", "default/c", TCP, 80, false)
	requireEvaluate(t, explicitEgressField, "default/c", "default/b", TCP, 80, false)

	explicitEgressOnly := mustCompile(t, cluster, NetworkPolicy{
		Name:        "explicit-egress",
		Namespace:   "default",
		PodSelector: LabelSelector{"app": "c"},
		PolicyTypes: []PolicyType{Egress},
		Egress:      []EgressRule{{}},
	})
	requireEvaluate(t, explicitEgressOnly, "default/a", "default/c", TCP, 80, true)
	requireEvaluate(t, explicitEgressOnly, "default/c", "default/a", TCP, 80, true)

	explicitIngressWithEgressRules := mustCompile(t, cluster, NetworkPolicy{
		Name:        "explicit-ingress",
		Namespace:   "default",
		PodSelector: LabelSelector{"app": "target"},
		PolicyTypes: []PolicyType{Ingress},
		Egress:      []EgressRule{},
	})
	got, err := explicitIngressWithEgressRules.IsIsolated("default/target", Egress)
	if err != nil || got {
		t.Fatalf("explicit Ingress-only policy must not isolate egress, got %v, %v", got, err)
	}
}

func TestMultiplePoliciesAreUnioned(t *testing.T) {
	cluster := testCluster(t)
	engine := mustCompile(t, cluster,
		NetworkPolicy{
			Name:        "from-a",
			Namespace:   "default",
			PodSelector: LabelSelector{"app": "target"},
			PolicyTypes: []PolicyType{Ingress},
			Ingress: []IngressRule{{
				From: []Peer{{PodSelector: LabelSelector{"app": "a"}}},
			}},
		},
		NetworkPolicy{
			Name:        "from-b",
			Namespace:   "default",
			PodSelector: LabelSelector{"app": "target"},
			PolicyTypes: []PolicyType{Ingress},
			Ingress: []IngressRule{{
				From: []Peer{{PodSelector: LabelSelector{"app": "b"}}},
			}},
		},
	)
	requireEvaluate(t, engine, "default/a", "default/target", TCP, 80, true)
	requireEvaluate(t, engine, "default/b", "default/target", TCP, 80, true)
	requireEvaluate(t, engine, "default/c", "default/target", TCP, 80, false)
}

func TestLoopbackIsAlwaysAllowed(t *testing.T) {
	cluster := testCluster(t)
	engine := mustCompile(t, cluster,
		NetworkPolicy{
			Name:        "closed-ingress",
			Namespace:   "default",
			PodSelector: LabelSelector{"app": "target"},
			PolicyTypes: []PolicyType{Ingress},
			Ingress:     []IngressRule{},
		},
		NetworkPolicy{
			Name:        "closed-egress",
			Namespace:   "default",
			PodSelector: LabelSelector{"app": "target"},
			PolicyTypes: []PolicyType{Egress},
			Egress:      []EgressRule{},
		},
	)
	requireEvaluate(t, engine, "default/target", "default/target", TCP, 80, true)
	requireEvaluate(t, engine, "default/target", "10.0.0.10", TCP, 12345, true)
	requireEvaluate(t, engine, "10.0.0.10", "default/target", TCP, 12345, true)
	requireEvaluate(t, engine, netip.MustParseAddr("10.0.0.10"), netip.MustParseAddr("10.0.0.10"), UDP, 53, true)
}

func TestNamespaceSelectorEmptyMatchesAllNamespaces(t *testing.T) {
	cluster := testCluster(t)
	if err := cluster.AddPod(Pod{
		Name: "unlabeled", Namespace: "team-a", Labels: map[string]string{"role": "worker"}, IP: "10.1.0.9",
	}); err != nil {
		t.Fatalf("add pod: %v", err)
	}
	engine := mustCompile(t, cluster, NetworkPolicy{
		Name:        "all-namespaces",
		Namespace:   "default",
		PodSelector: LabelSelector{"app": "target"},
		PolicyTypes: []PolicyType{Ingress},
		Ingress: []IngressRule{{
			From: []Peer{{
				NamespaceSelector: LabelSelector{},
				PodSelector:       LabelSelector{"role": "worker"},
			}},
		}},
	})
	requireEvaluate(t, engine, "team-a/unlabeled", "default/target", TCP, 80, true)
}

func TestInvalidSelectorPeerCombination(t *testing.T) {
	cluster := testCluster(t)
	_, err := Compile(cluster, []NetworkPolicy{{
		Namespace:   "default",
		PodSelector: LabelSelector{"app": "target"},
		Ingress: []IngressRule{{
			From: []Peer{{
				IPBlock:     &IPBlock{CIDR: "10.0.0.0/8"},
				PodSelector: LabelSelector{"app": "a"},
			}},
		}},
	}})
	if err == nil || !strings.Contains(err.Error(), "ipBlock") {
		t.Fatalf("expected ipBlock/selector validation error, got %v", err)
	}
}
