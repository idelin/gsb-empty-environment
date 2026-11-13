package netsec

import (
	"testing"
)

func TestSecretOverriddenByBroadRequestResponseRule(t *testing.T) {
	rules := []Rule{
		{
			Level:     LevelRequestResponse,
			Resources: []GroupResources{{Resources: []string{"*"}}},
		},
		{
			Level:     LevelMetadata,
			Resources: []GroupResources{{Resources: []string{"secrets"}}},
		},
	}
	probes := []Probe{
		{User: "alice", Verb: "get", Namespace: "default", Resource: "secrets", ResourceName: "token"},
	}

	level, ruleIndex := Resolve(rules, probes[0])
	if level != LevelRequestResponse || ruleIndex != 0 {
		t.Fatalf("Resolve() = (%q, %d), want (%q, 0)", level, ruleIndex, LevelRequestResponse)
	}

	findings := Check(rules, probes)
	if len(findings) != 2 {
		t.Fatalf("Check() returned %d findings, want 2: %+v", len(findings), findings)
	}
	if findings[0].Kind != KindSensitiveLevel || findings[0].RuleIndex != 0 || findings[0].Level != LevelRequestResponse {
		t.Fatalf("first finding = %+v, want sensitive level from rule 0", findings[0])
	}
	if findings[1].Kind != KindShadowedRule || findings[1].RuleIndex != 1 {
		t.Fatalf("second finding = %+v, want shadowed rule 1", findings[1])
	}
}

func TestSubresourceWildcardMatching(t *testing.T) {
	rule := Rule{
		Level:     LevelMetadata,
		Resources: []GroupResources{{Resources: []string{"secrets/*"}}},
	}

	matching := []Probe{
		{Resource: "secrets/status"},
		{Resource: "secrets", Subresource: "token"},
		{Resource: "secrets/log"},
	}
	for _, probe := range matching {
		if !MatchRule(rule, probe) {
			t.Errorf("MatchRule(%+v) = false, want true", probe)
		}
	}

	if MatchRule(rule, Probe{Resource: "secrets"}) {
		t.Errorf("secrets/* unexpectedly matched the base secrets resource")
	}
	if MatchRule(rule, Probe{Resource: "configmaps/status"}) {
		t.Errorf("secrets/* unexpectedly matched configmaps/status")
	}
}

func TestWildcardResourceAndEmptyFields(t *testing.T) {
	rule := Rule{
		Level:     LevelMetadata,
		Resources: []GroupResources{{Resources: []string{"*"}}},
	}
	for _, probe := range []Probe{
		{Resource: "secrets"},
		{Resource: "secrets/status"},
		{APIGroup: "apps", Resource: "deployments"},
	} {
		if !MatchRule(rule, probe) {
			t.Errorf("wildcard resource did not match %+v", probe)
		}
	}

	empty := Rule{Level: LevelMetadata}
	if !MatchRule(empty, Probe{User: "alice", Verb: "delete", Resource: "widgets"}) {
		t.Error("rule with empty fields should match any request")
	}
}

func TestUserGroupVerbNamespaceAndResourceNameConstraints(t *testing.T) {
	rule := Rule{
		Level:      LevelMetadata,
		Users:      []string{"alice"},
		UserGroups: []string{"developers"},
		Verbs:      []string{"get"},
		Namespaces: []string{"default"},
		Resources: []GroupResources{{
			Group:         "apps",
			Resources:     []string{"deployments"},
			ResourceNames: []string{"web"},
		}},
	}
	matching := Probe{
		User:         "alice",
		UserGroups:   []string{"viewers", "developers"},
		Verb:         "get",
		Namespace:    "default",
		APIGroup:     "apps",
		Resource:     "deployments",
		ResourceName: "web",
	}
	if !MatchRule(rule, matching) {
		t.Fatalf("MatchRule(%+v) = false, want true", matching)
	}

	mismatches := []Probe{
		{User: "bob", UserGroups: matching.UserGroups, Verb: "get", Namespace: "default", APIGroup: "apps", Resource: "deployments", ResourceName: "web"},
		{User: "alice", UserGroups: []string{"viewers"}, Verb: "get", Namespace: "default", APIGroup: "apps", Resource: "deployments", ResourceName: "web"},
		{User: "alice", UserGroups: matching.UserGroups, Verb: "delete", Namespace: "default", APIGroup: "apps", Resource: "deployments", ResourceName: "web"},
		{User: "alice", UserGroups: matching.UserGroups, Verb: "get", Namespace: "other", APIGroup: "apps", Resource: "deployments", ResourceName: "web"},
		{User: "alice", UserGroups: matching.UserGroups, Verb: "get", Namespace: "default", APIGroup: "batch", Resource: "deployments", ResourceName: "web"},
		{User: "alice", UserGroups: matching.UserGroups, Verb: "get", Namespace: "default", APIGroup: "apps", Resource: "deployments", ResourceName: "api"},
	}
	for _, probe := range mismatches {
		if MatchRule(rule, probe) {
			t.Errorf("MatchRule(%+v) = true, want false", probe)
		}
	}
}

func TestShadowedRuleByUnionOfEarlierRules(t *testing.T) {
	rules := []Rule{
		{Level: LevelMetadata, Users: []string{"alice"}, Resources: []GroupResources{{Resources: []string{"secrets"}}}},
		{Level: LevelMetadata, Users: []string{"bob"}, Resources: []GroupResources{{Resources: []string{"secrets"}}}},
		{Level: LevelMetadata, Users: []string{"alice", "bob"}, Resources: []GroupResources{{Resources: []string{"secrets"}}}},
	}

	findings := Check(rules, nil)
	if len(findings) != 1 {
		t.Fatalf("Check() returned %d findings, want only the shadowed rule: %+v", len(findings), findings)
	}
	if findings[0].Kind != KindShadowedRule || findings[0].RuleIndex != 2 {
		t.Fatalf("finding = %+v, want rule 2 shadowed", findings[0])
	}
}

func TestEmptyRuleShadowsEverythingAfterIt(t *testing.T) {
	rules := []Rule{
		{Level: LevelMetadata},
		{Level: LevelRequestResponse, Users: []string{"alice"}, Resources: []GroupResources{{Resources: []string{"secrets"}}}},
		{Level: LevelMetadata, NonResourceURLs: []string{"/healthz/*"}},
	}

	findings := Check(rules, nil)
	if len(findings) != 2 {
		t.Fatalf("Check() returned %d findings, want 2 shadowed rules: %+v", len(findings), findings)
	}
	for _, finding := range findings {
		if finding.Kind != KindShadowedRule {
			t.Fatalf("finding = %+v, want shadowed-rule", finding)
		}
	}
}

func TestSubresourceWildcardShadowsSpecificSubresource(t *testing.T) {
	rules := []Rule{
		{Level: LevelMetadata, Resources: []GroupResources{{Resources: []string{"secrets/*"}}}},
		{Level: LevelMetadata, Resources: []GroupResources{{Resources: []string{"secrets/status"}}}},
	}

	findings := Check(rules, nil)
	if len(findings) != 1 || findings[0].RuleIndex != 1 {
		t.Fatalf("Check() = %+v, want rule 1 shadowed by secrets/*", findings)
	}
}

func TestSpecificSubresourceDoesNotShadowSubresourceWildcard(t *testing.T) {
	rules := []Rule{
		{Level: LevelMetadata, Resources: []GroupResources{{Resources: []string{"secrets/status"}}}},
		{Level: LevelMetadata, Resources: []GroupResources{{Resources: []string{"secrets/*"}}}},
	}

	if findings := Check(rules, nil); len(findings) != 0 {
		t.Fatalf("Check() = %+v, want no shadowed rule", findings)
	}
}

func TestMissingDefaultRule(t *testing.T) {
	rules := []Rule{
		{Level: LevelMetadata, Resources: []GroupResources{{Resources: []string{"secrets"}}}},
	}
	probes := []Probe{
		{User: "alice", Verb: "get", Namespace: "default", Resource: "configmaps", ResourceName: "app-config"},
	}

	level, ruleIndex := Resolve(rules, probes[0])
	if level != LevelNone || ruleIndex != -1 {
		t.Fatalf("Resolve() = (%q, %d), want (None, -1)", level, ruleIndex)
	}

	findings := Check(rules, probes)
	if len(findings) != 1 {
		t.Fatalf("Check() returned %d findings, want 1: %+v", len(findings), findings)
	}
	if findings[0].Kind != KindMissingDefault || findings[0].ProbeIndex != 0 {
		t.Fatalf("finding = %+v, want missing default for probe 0", findings[0])
	}
}

func TestSensitiveResourcesRejectRequestLevels(t *testing.T) {
	tests := []struct {
		name     string
		probe    Probe
		resource string
	}{
		{
			name:     "secret subresource",
			probe:    Probe{Resource: "secrets", Subresource: "status"},
			resource: "secrets/status",
		},
		{
			name:     "configmap subresource",
			probe:    Probe{Resource: "configmaps/status"},
			resource: "configmaps/status",
		},
		{
			name:     "token review",
			probe:    Probe{Resource: "tokenreviews"},
			resource: "tokenreviews",
		},
		{
			name:     "service account token",
			probe:    Probe{Resource: "serviceaccounts", Subresource: "token"},
			resource: "serviceaccounts/token",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rules := []Rule{
				{Level: LevelRequest, Resources: []GroupResources{{Resources: []string{"*"}}}},
			}
			findings := Check(rules, []Probe{test.probe})
			if len(findings) != 1 {
				t.Fatalf("Check() returned %d findings, want 1: %+v", len(findings), findings)
			}
			if findings[0].Kind != KindSensitiveLevel || findings[0].Resource != test.resource {
				t.Fatalf("finding = %+v, want sensitive-level for %q", findings[0], test.resource)
			}
		})
	}
}

func TestNonSensitiveResourceMayUseRequestLevel(t *testing.T) {
	rules := []Rule{
		{Level: LevelRequestResponse, Resources: []GroupResources{{Resources: []string{"*"}}}},
	}
	probes := []Probe{
		{Resource: "pods"},
		{Resource: "serviceaccounts"},
	}

	if findings := Check(rules, probes); len(findings) != 0 {
		t.Fatalf("Check() = %+v, want no findings", findings)
	}
}

func TestNonResourceURLMatching(t *testing.T) {
	rule := Rule{
		Level:           LevelMetadata,
		NonResourceURLs: []string{"/healthz/*"},
	}
	if !MatchRule(rule, Probe{NonResourceURL: "/healthz/live"}) {
		t.Error("/healthz/* should match /healthz/live")
	}
	if MatchRule(rule, Probe{NonResourceURL: "/healthz"}) {
		t.Error("/healthz/* should not match /healthz")
	}
	if MatchRule(rule, Probe{Resource: "pods"}) {
		t.Error("non-resource URL rule should not match a resource request")
	}
}

func TestAnalyzeGroupsFindings(t *testing.T) {
	rules := []Rule{
		{Level: LevelRequestResponse, Users: []string{"alice"}, Resources: []GroupResources{{Resources: []string{"*"}}}},
		{Level: LevelMetadata, Users: []string{"bob"}, Resources: []GroupResources{{Resources: []string{"secrets"}}}},
		{Level: LevelRequestResponse, Users: []string{"carol"}, Resources: []GroupResources{{Resources: []string{"configmaps"}}}},
	}
	probes := []Probe{
		{User: "alice", Resource: "secrets"},
		{User: "carol", Resource: "configmaps"},
		{User: "dave", Resource: "tokenreviews"},
	}

	report := Analyze(rules, probes)
	if report.OK() {
		t.Fatal("Analyze() reported OK for a policy with findings")
	}
	if len(report.SensitiveLevelFindings) != 2 {
		t.Fatalf("sensitive findings = %+v, want 2", report.SensitiveLevelFindings)
	}
	if len(report.MissingDefaultFindings) != 1 {
		t.Fatalf("missing-default findings = %+v, want 1", report.MissingDefaultFindings)
	}
	if len(report.Findings) != 3 {
		t.Fatalf("total findings = %d, want 3", len(report.Findings))
	}
}
