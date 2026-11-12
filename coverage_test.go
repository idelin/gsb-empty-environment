package netsec

import "testing"

// testCluster 模拟集群启用的资源版本表：
// pods 启用了 v1（含 exec、ephemeralcontainers 子资源）；
// deployments 同时启用 apps/v1 与旧版 apps/v1beta1，二者互为等价版本。
var testCluster = []EnabledResource{
	{
		Group:        "",
		Version:      "v1",
		Resource:     "pods",
		Subresources: []string{"exec", "ephemeralcontainers"},
		Namespaced:   true,
	},
	{
		Group:              "apps",
		Version:            "v1",
		Resource:           "deployments",
		Namespaced:         true,
		EquivalentVersions: []string{"v1beta1"},
	},
	{
		Group:              "apps",
		Version:            "v1beta1",
		Resource:           "deployments",
		Namespaced:         true,
		EquivalentVersions: []string{"v1"},
	},
}

func findGap(gaps []Gap, resource, subresource, operation string) *Gap {
	for i := range gaps {
		g := &gaps[i]
		if g.Resource == resource && g.Subresource == subresource && g.Operation == operation {
			return g
		}
	}
	return nil
}

// TestSubresourceOmission 验证 "*" 与 "pods" 只匹配主资源，
// 子资源必须显式写 "pods/*" 或 "*/*" 才算覆盖。
func TestSubresourceOmission(t *testing.T) {
	reqs := []Requirement{
		{Group: "", Resource: "pods", Operations: []string{"CREATE"}},
		{Group: "", Resource: "pods", Subresource: "ephemeralcontainers", Operations: []string{"CREATE"}},
	}

	// 只写主资源：子资源遗漏。
	spec := PolicySpec{
		MatchPolicy: MatchPolicyExact,
		MatchRules: []MatchRule{{
			APIGroups:   []string{""},
			APIVersions: []string{"v1"},
			Operations:  []string{"CREATE"},
			Resources:   []string{"pods"},
		}},
	}
	gaps := AnalyzeCoverage(spec, testCluster, reqs)
	if findGap(gaps, "pods", "", "CREATE") != nil {
		t.Fatalf("主资源 pods CREATE 应被覆盖, gaps=%v", gaps)
	}
	if g := findGap(gaps, "pods", "ephemeralcontainers", "CREATE"); g == nil {
		t.Fatal("pods/ephemeralcontainers CREATE 应报告未覆盖")
	}

	// "*" 同样只匹配主资源。
	spec.MatchRules[0].Resources = []string{"*"}
	gaps = AnalyzeCoverage(spec, testCluster, reqs)
	if g := findGap(gaps, "pods", "ephemeralcontainers", "CREATE"); g == nil {
		t.Fatal(`resources ["*"] 不应覆盖子资源`)
	}

	// "pods/*" 覆盖主资源与全部子资源。
	spec.MatchRules[0].Resources = []string{"pods/*"}
	if gaps := AnalyzeCoverage(spec, testCluster, reqs); len(gaps) != 0 {
		t.Fatalf(`resources ["pods/*"] 应完整覆盖, gaps=%v`, gaps)
	}
}

// TestLegacyVersion 验证 Exact 下旧版本不算覆盖，
// Equivalent 下按集群版本表的等价版本补全。
func TestLegacyVersion(t *testing.T) {
	reqs := []Requirement{
		{Group: "apps", Resource: "deployments", Operations: []string{"CREATE"}},
	}
	spec := PolicySpec{
		MatchPolicy: MatchPolicyExact,
		MatchRules: []MatchRule{{
			APIGroups:   []string{"apps"},
			APIVersions: []string{"v1"},
			Operations:  []string{"CREATE"},
			Resources:   []string{"deployments"},
		}},
	}

	// Exact：v1beta1 未被覆盖。
	gaps := AnalyzeCoverage(spec, testCluster, reqs)
	g := findGap(gaps, "deployments", "", "CREATE")
	if g == nil {
		t.Fatal("Exact 下旧版本 v1beta1 应报告未覆盖")
	}
	if len(g.Versions) != 1 || g.Versions[0] != "v1beta1" {
		t.Fatalf("未覆盖版本应为 [v1beta1], got %v", g.Versions)
	}

	// Equivalent：v1 规则按等价版本补全，覆盖 v1beta1。
	spec.MatchPolicy = MatchPolicyEquivalent
	if gaps := AnalyzeCoverage(spec, testCluster, reqs); len(gaps) != 0 {
		t.Fatalf("Equivalent 下等价版本应补全覆盖, gaps=%v", gaps)
	}
}

// TestCreateOnly 验证只对 CREATE 生效的校验无法覆盖其他操作。
func TestCreateOnly(t *testing.T) {
	reqs := []Requirement{
		{Group: "", Resource: "pods", Operations: []string{"CREATE", "UPDATE", "DELETE"}},
	}
	spec := PolicySpec{
		MatchPolicy: MatchPolicyExact,
		MatchRules: []MatchRule{{
			APIGroups:   []string{""},
			APIVersions: []string{"v1"},
			Operations:  []string{"CREATE"},
			Resources:   []string{"pods"},
		}},
	}
	gaps := AnalyzeCoverage(spec, testCluster, reqs)
	if findGap(gaps, "pods", "", "CREATE") != nil {
		t.Fatalf("CREATE 应被覆盖, gaps=%v", gaps)
	}
	for _, op := range []string{"UPDATE", "DELETE"} {
		if findGap(gaps, "pods", "", op) == nil {
			t.Fatalf("仅 CREATE 的策略不应覆盖 %s", op)
		}
	}
}

// TestExcludeRuleSubtraction 验证排除规则先从包含规则里扣除。
func TestExcludeRuleSubtraction(t *testing.T) {
	reqs := []Requirement{
		{Group: "", Resource: "pods", Operations: []string{"*"}},
		{Group: "", Resource: "pods", Subresource: "exec", Operations: []string{"CONNECT"}},
	}
	spec := PolicySpec{
		MatchPolicy: MatchPolicyExact,
		MatchRules: []MatchRule{{
			APIGroups:   []string{""},
			APIVersions: []string{"v1"},
			Operations:  []string{"*"},
			Resources:   []string{"pods/*"},
		}},
		ExcludeRules: []MatchRule{{
			APIGroups:   []string{""},
			APIVersions: []string{"v1"},
			Operations:  []string{"CONNECT"},
			Resources:   []string{"pods/exec"},
		}},
	}
	gaps := AnalyzeCoverage(spec, testCluster, reqs)
	if g := findGap(gaps, "pods", "exec", "CONNECT"); g == nil {
		t.Fatal("排除规则扣除后 pods/exec CONNECT 应报告未覆盖")
	}
	// 主资源及其余操作仍被包含规则覆盖。
	for _, op := range []string{"CREATE", "UPDATE", "DELETE", "CONNECT"} {
		if findGap(gaps, "pods", "", op) != nil {
			t.Fatalf("主资源 pods %s 应仍被覆盖, gaps=%v", op, gaps)
		}
	}
}

// TestFullCoverage 验证完整覆盖时不报告任何未覆盖项。
func TestFullCoverage(t *testing.T) {
	reqs := []Requirement{
		{Group: "", Resource: "pods", Operations: []string{"*"}},
		{Group: "", Resource: "pods", Subresource: "exec", Operations: []string{"CONNECT"}},
		{Group: "", Resource: "pods", Subresource: "ephemeralcontainers", Operations: []string{"CREATE", "UPDATE"}},
		{Group: "apps", Resource: "deployments", Operations: []string{"*"}},
	}
	spec := PolicySpec{
		MatchPolicy: MatchPolicyEquivalent,
		MatchRules: []MatchRule{
			{
				APIGroups:   []string{""},
				APIVersions: []string{"v1"},
				Operations:  []string{"*"},
				Resources:   []string{"pods/*"},
			},
			{
				APIGroups:   []string{"apps"},
				APIVersions: []string{"v1"},
				Operations:  []string{"*"},
				Resources:   []string{"deployments"},
			},
		},
	}
	if gaps := AnalyzeCoverage(spec, testCluster, reqs); len(gaps) != 0 {
		t.Fatalf("应完整覆盖, gaps=%v", gaps)
	}
}

// TestScopeFilter 验证 scope 限定规则作用的资源范围。
func TestScopeFilter(t *testing.T) {
	cluster := append([]EnabledResource{}, testCluster...)
	cluster = append(cluster, EnabledResource{
		Group:      "rbac.authorization.k8s.io",
		Version:    "v1",
		Resource:   "clusterroles",
		Namespaced: false,
	})
	reqs := []Requirement{
		{Group: "rbac.authorization.k8s.io", Resource: "clusterroles", Operations: []string{"CREATE"}},
	}
	spec := PolicySpec{
		MatchPolicy: MatchPolicyExact,
		MatchRules: []MatchRule{{
			APIGroups:   []string{"rbac.authorization.k8s.io"},
			APIVersions: []string{"v1"},
			Operations:  []string{"CREATE"},
			Resources:   []string{"clusterroles"},
			Scope:       ScopeNamespaced, // 只作用于命名空间资源
		}},
	}
	if gaps := AnalyzeCoverage(spec, cluster, reqs); findGap(gaps, "clusterroles", "", "CREATE") == nil {
		t.Fatal("scope=Namespaced 的规则不应覆盖集群级资源")
	}
	spec.MatchRules[0].Scope = ScopeCluster
	if gaps := AnalyzeCoverage(spec, cluster, reqs); len(gaps) != 0 {
		t.Fatalf("scope=Cluster 应覆盖 clusterroles, gaps=%v", gaps)
	}
}
