package netsec

import (
	"reflect"
	"sort"
	"testing"
)

func testNamespace(name string, labels map[string]string) Namespace {
	return Namespace{Name: name, Labels: labels}
}

func testPod(namespace string, labels map[string]string) Pod {
	return Pod{
		Name:      "pod",
		Namespace: testNamespace(namespace, nil),
		Labels:    labels,
	}
}

func selectorWithLabels(labels map[string]string) LabelSelector {
	return LabelSelector{MatchLabels: labels}
}

func selectorPtr(selector LabelSelector) *LabelSelector {
	return &selector
}

func basePolicy() NetworkPolicy {
	return NetworkPolicy{
		Namespace:   "payments",
		PodSelector: selectorWithLabels(map[string]string{"app": "web"}),
	}
}

func mutableDependencyKeys(decision Decision) []string {
	keys := make([]string, 0, len(decision.MutableNamespaceLabels))
	for _, dependency := range decision.MutableNamespaceLabels {
		keys = append(keys, dependency.Key)
	}
	sort.Strings(keys)
	return keys
}

func TestDestinationMustBeSelectedByPolicy(t *testing.T) {
	policy := basePolicy()
	policy.Ingress = []IngressRule{{}}
	source := testPod("payments", map[string]string{"role": "client"})

	tests := []struct {
		name        string
		destination Pod
		allowed     bool
	}{
		{
			name:        "destination in policy namespace and selected",
			destination: testPod("payments", map[string]string{"app": "web"}),
			allowed:     true,
		},
		{
			name:        "destination outside policy namespace",
			destination: testPod("shipping", map[string]string{"app": "web"}),
			allowed:     false,
		},
		{
			name:        "destination not selected by policy pod selector",
			destination: testPod("payments", map[string]string{"app": "db"}),
			allowed:     false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision := EvaluateIngress(policy, source, test.destination)
			if decision.Allowed != test.allowed {
				t.Fatalf("Allowed = %v, want %v; decision = %+v", decision.Allowed, test.allowed, decision)
			}
		})
	}
}

func TestNoIngressRulesDeniesAllSources(t *testing.T) {
	policy := basePolicy()
	source := testPod("payments", map[string]string{"role": "client"})
	destination := testPod("payments", map[string]string{"app": "web"})

	decision := EvaluateIngress(policy, source, destination)
	if decision.Allowed {
		t.Fatalf("Allowed = true with no ingress rules; decision = %+v", decision)
	}
}

func TestRuleWithoutFromAllowsAllSources(t *testing.T) {
	policy := basePolicy()
	policy.Ingress = []IngressRule{{}}
	source := testPod("attacker", map[string]string{"role": "client"})
	destination := testPod("payments", map[string]string{"app": "web"})

	decision := EvaluateIngress(policy, source, destination)
	if !decision.Allowed {
		t.Fatalf("Allowed = false, want true; decision = %+v", decision)
	}
	if len(decision.Matches) != 1 || decision.Matches[0].Peer != -1 {
		t.Fatalf("Matches = %+v, want one open rule match", decision.Matches)
	}
	if decision.ReliesOnMutableNamespaceLabels {
		t.Fatalf("open rule unexpectedly depends on mutable namespace labels: %+v", decision)
	}
}

func TestSameFromElementIntersectsNamespaceAndPodSelectors(t *testing.T) {
	policy := basePolicy()
	policy.Ingress = []IngressRule{{
		From: []PeerSelector{{
			NamespaceSelector: selectorPtr(selectorWithLabels(map[string]string{
				MetadataNameLabel: "payments",
			})),
			PodSelector: selectorPtr(selectorWithLabels(map[string]string{
				"role": "client",
			})),
		}},
	}}
	destination := testPod("payments", map[string]string{"app": "web"})

	tests := []struct {
		name      string
		namespace string
		labels    map[string]string
		allowed   bool
	}{
		{
			name:      "both selectors match",
			namespace: "payments",
			labels:    map[string]string{"role": "client"},
			allowed:   true,
		},
		{
			name:      "namespace matches but pod selector does not",
			namespace: "payments",
			labels:    map[string]string{"role": "worker"},
			allowed:   false,
		},
		{
			name:      "pod selector matches but namespace does not",
			namespace: "shipping",
			labels:    map[string]string{"role": "client"},
			allowed:   false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := testPod(test.namespace, test.labels)
			decision := EvaluateIngress(policy, source, destination)
			if decision.Allowed != test.allowed {
				t.Fatalf("Allowed = %v, want %v; decision = %+v", decision.Allowed, test.allowed, decision)
			}
		})
	}
}

func TestDifferentFromElementsAreUnioned(t *testing.T) {
	policy := basePolicy()
	policy.Ingress = []IngressRule{{
		From: []PeerSelector{
			{
				NamespaceSelector: selectorPtr(selectorWithLabels(map[string]string{
					MetadataNameLabel: "payments",
				})),
				PodSelector: selectorPtr(selectorWithLabels(map[string]string{
					"role": "client",
				})),
			},
			{
				NamespaceSelector: selectorPtr(selectorWithLabels(map[string]string{
					MetadataNameLabel: "shipping",
				})),
				PodSelector: selectorPtr(selectorWithLabels(map[string]string{
					"role": "worker",
				})),
			},
		},
	}}
	destination := testPod("payments", map[string]string{"app": "web"})

	tests := []struct {
		name      string
		namespace string
		role      string
		allowed   bool
	}{
		{"first element matches", "payments", "client", true},
		{"second element matches", "shipping", "worker", true},
		{"conditions from different elements are not combined", "payments", "worker", false},
		{"second namespace with first pod role is not combined", "shipping", "client", false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := testPod(test.namespace, map[string]string{"role": test.role})
			decision := EvaluateIngress(policy, source, destination)
			if decision.Allowed != test.allowed {
				t.Fatalf("Allowed = %v, want %v; decision = %+v", decision.Allowed, test.allowed, decision)
			}
		})
	}
}

func TestSplitFromElementsDoNotMeanIntersection(t *testing.T) {
	source := testPod("payments", map[string]string{"role": "worker"})
	destination := testPod("payments", map[string]string{"app": "web"})

	sameElementPolicy := basePolicy()
	sameElementPolicy.Ingress = []IngressRule{{
		From: []PeerSelector{{
			NamespaceSelector: selectorPtr(selectorWithLabels(map[string]string{
				MetadataNameLabel: "payments",
			})),
			PodSelector: selectorPtr(selectorWithLabels(map[string]string{
				"role": "client",
			})),
		}},
	}}

	splitElementPolicy := basePolicy()
	splitElementPolicy.Ingress = []IngressRule{{
		From: []PeerSelector{
			{
				NamespaceSelector: selectorPtr(selectorWithLabels(map[string]string{
					MetadataNameLabel: "payments",
				})),
			},
			{
				PodSelector: selectorPtr(selectorWithLabels(map[string]string{
					"role": "client",
				})),
			},
		},
	}}

	if decision := EvaluateIngress(sameElementPolicy, source, destination); decision.Allowed {
		t.Fatalf("same-element selectors must intersect, got allow: %+v", decision)
	}
	if decision := EvaluateIngress(splitElementPolicy, source, destination); !decision.Allowed {
		t.Fatalf("split-element selectors must union, got deny: %+v", decision)
	}
}

func TestPodSelectorAloneIsScopedToPolicyNamespace(t *testing.T) {
	policy := basePolicy()
	policy.Ingress = []IngressRule{{
		From: []PeerSelector{{
			PodSelector: selectorPtr(selectorWithLabels(map[string]string{
				"role": "client",
			})),
		}},
	}}
	destination := testPod("payments", map[string]string{"app": "web"})

	inside := EvaluateIngress(policy, testPod("payments", map[string]string{"role": "client"}), destination)
	if !inside.Allowed {
		t.Fatalf("source in policy namespace should be allowed: %+v", inside)
	}

	outside := EvaluateIngress(policy, testPod("shipping", map[string]string{"role": "client"}), destination)
	if outside.Allowed {
		t.Fatalf("podSelector alone must not match a source in another namespace: %+v", outside)
	}
}

func TestEmptySelectors(t *testing.T) {
	destination := testPod("payments", map[string]string{"app": "web"})

	tests := []struct {
		name     string
		peer     PeerSelector
		sourceNS string
		allowed  bool
	}{
		{
			name:     "empty namespace selector matches every namespace",
			peer:     PeerSelector{NamespaceSelector: selectorPtr(LabelSelector{})},
			sourceNS: "attacker",
			allowed:  true,
		},
		{
			name:     "empty pod selector alone is limited to policy namespace",
			peer:     PeerSelector{PodSelector: selectorPtr(LabelSelector{})},
			sourceNS: "attacker",
			allowed:  false,
		},
		{
			name:     "empty pod selector in policy namespace",
			peer:     PeerSelector{PodSelector: selectorPtr(LabelSelector{})},
			sourceNS: "payments",
			allowed:  true,
		},
		{
			name:     "empty peer matches every namespace and pod",
			peer:     PeerSelector{},
			sourceNS: "attacker",
			allowed:  true,
		},
		{
			name: "both empty selectors match every namespace and pod",
			peer: PeerSelector{
				NamespaceSelector: selectorPtr(LabelSelector{}),
				PodSelector:       selectorPtr(LabelSelector{}),
			},
			sourceNS: "attacker",
			allowed:  true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := basePolicy()
			policy.Ingress = []IngressRule{{From: []PeerSelector{test.peer}}}
			source := testPod(test.sourceNS, map[string]string{"role": "client"})

			decision := EvaluateIngress(policy, source, destination)
			if decision.Allowed != test.allowed {
				t.Fatalf("Allowed = %v, want %v; decision = %+v", decision.Allowed, test.allowed, decision)
			}
			if decision.ReliesOnMutableNamespaceLabels {
				t.Fatalf("empty selectors should have no mutable label dependency: %+v", decision)
			}
		})
	}
}

func TestMutableNamespaceLabelUsedForAllowIsReported(t *testing.T) {
	policy := basePolicy()
	policy.Ingress = []IngressRule{{
		From: []PeerSelector{{
			NamespaceSelector: selectorPtr(selectorWithLabels(map[string]string{
				"team": "payments",
			})),
			PodSelector: selectorPtr(selectorWithLabels(map[string]string{
				"role": "client",
			})),
		}},
	}}
	destination := testPod("payments", map[string]string{"app": "web"})
	source := Pod{
		Name: "attacker-pod",
		Namespace: testNamespace("attacker", map[string]string{
			MetadataNameLabel: "attacker",
			"team":            "payments",
		}),
		Labels: map[string]string{"role": "client"},
	}

	decision := EvaluateIngress(policy, source, destination)
	if !decision.Allowed {
		t.Fatalf("Allowed = false, want true; decision = %+v", decision)
	}
	if !decision.ReliesOnMutableNamespaceLabels {
		t.Fatalf("allow depending on team label was not flagged: %+v", decision)
	}
	if got, want := mutableDependencyKeys(decision), []string{"team"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("mutable dependency keys = %v, want %v", got, want)
	}
	if len(decision.MutableNamespaceLabels) != 1 {
		t.Fatalf("MutableNamespaceLabels = %+v, want one entry", decision.MutableNamespaceLabels)
	}
	dependency := decision.MutableNamespaceLabels[0]
	if dependency.Namespace != "attacker" || dependency.Key != "team" ||
		dependency.Operator != LabelSelectorOpIn ||
		!reflect.DeepEqual(dependency.Values, []string{"payments"}) {
		t.Fatalf("dependency = %+v, want attacker/team In payments", dependency)
	}
}

func TestImmutableMetadataNameDoesNotFlagDependency(t *testing.T) {
	policy := basePolicy()
	policy.Ingress = []IngressRule{{
		From: []PeerSelector{{
			NamespaceSelector: selectorPtr(selectorWithLabels(map[string]string{
				MetadataNameLabel: "payments",
			})),
		}},
	}}
	destination := testPod("payments", map[string]string{"app": "web"})
	source := testPod("payments", map[string]string{"role": "client"})

	decision := EvaluateIngress(policy, source, destination)
	if !decision.Allowed {
		t.Fatalf("Allowed = false, want true; decision = %+v", decision)
	}
	if decision.ReliesOnMutableNamespaceLabels || len(decision.MutableNamespaceLabels) != 0 {
		t.Fatalf("metadata.name is immutable and must not be flagged: %+v", decision)
	}
}

func TestOnlyMutableLabelPredicatesAreReported(t *testing.T) {
	policy := basePolicy()
	policy.Ingress = []IngressRule{{
		From: []PeerSelector{{
			NamespaceSelector: &LabelSelector{
				MatchLabels: map[string]string{
					MetadataNameLabel: "payments",
					"team":            "payments",
				},
			},
		}},
	}}
	destination := testPod("payments", map[string]string{"app": "web"})
	source := testPod("payments", map[string]string{"role": "client"})
	source.Namespace.Labels = map[string]string{"team": "payments"}

	decision := EvaluateIngress(policy, source, destination)
	if !decision.Allowed || !decision.ReliesOnMutableNamespaceLabels {
		t.Fatalf("decision = %+v, want allow with mutable dependency", decision)
	}
	if got, want := mutableDependencyKeys(decision), []string{"team"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("mutable dependency keys = %v, want %v", got, want)
	}
}

func TestMutableLabelDependencyIsNotReportedOnDeny(t *testing.T) {
	policy := basePolicy()
	policy.Ingress = []IngressRule{{
		From: []PeerSelector{{
			NamespaceSelector: selectorPtr(selectorWithLabels(map[string]string{
				"team": "payments",
			})),
		}},
	}}
	destination := testPod("payments", map[string]string{"app": "web"})
	source := testPod("attacker", map[string]string{"role": "client"})
	source.Namespace.Labels = map[string]string{"team": "shipping"}

	decision := EvaluateIngress(policy, source, destination)
	if decision.Allowed {
		t.Fatalf("Allowed = true, want false; decision = %+v", decision)
	}
	if decision.ReliesOnMutableNamespaceLabels || len(decision.MutableNamespaceLabels) != 0 {
		t.Fatalf("denied decision must not report an allow dependency: %+v", decision)
	}
}

func TestMutableMatchExpressionDependencyIsReported(t *testing.T) {
	policy := basePolicy()
	policy.Ingress = []IngressRule{{
		From: []PeerSelector{{
			NamespaceSelector: &LabelSelector{
				MatchExpressions: []LabelSelectorRequirement{{
					Key:      "team",
					Operator: LabelSelectorOpDoesNotExist,
				}},
			},
		}},
	}}
	destination := testPod("payments", map[string]string{"app": "web"})
	source := testPod("attacker", map[string]string{"role": "client"})

	decision := EvaluateIngress(policy, source, destination)
	if !decision.Allowed || !decision.ReliesOnMutableNamespaceLabels {
		t.Fatalf("decision = %+v, want allow with mutable dependency", decision)
	}
	dependency := decision.MutableNamespaceLabels[0]
	if dependency.Key != "team" || dependency.Operator != LabelSelectorOpDoesNotExist {
		t.Fatalf("dependency = %+v, want team DoesNotExist", dependency)
	}
}

func TestNamespaceNameOverridesSpoofedMetadataLabel(t *testing.T) {
	namespace := Namespace{
		Name: "payments",
		Labels: map[string]string{
			MetadataNameLabel: "attacker",
		},
	}
	if got, want := NamespaceName(namespace), "payments"; got != want {
		t.Fatalf("NamespaceName = %q, want %q", got, want)
	}
	if got, want := NamespaceLabels(namespace)[MetadataNameLabel], "payments"; got != want {
		t.Fatalf("effective metadata name = %q, want %q", got, want)
	}
}
