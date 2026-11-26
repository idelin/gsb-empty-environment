package delegation_test

import (
	"testing"

	root "formal.local/h26-32a6cdd198"
)

func TestWildcardPatterns(t *testing.T) {
	u, _ := newWildcardUniverse(t)
	prover, err := root.NewProver(u)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		path     []string
		decision root.Effect
	}{
		{[]string{"a", "b"}, root.EffectDeny},
		{[]string{"a", "b", "c"}, root.EffectAllow},
		{[]string{"a", "b", "c", "a", "c"}, root.EffectAllow},
		{[]string{"a", "c", "b"}, root.EffectDeny},
	}
	for _, tc := range cases {
		explanation, err := prover.ExplainDelegation("leaf", root.Tuple{Path: tc.path, Tags: []string{"r"}, Timestamp: 1})
		if err != nil {
			t.Fatalf("ExplainDelegation(%v): %v", tc.path, err)
		}
		if explanation.Decision != tc.decision {
			t.Fatalf("path %v decision = %q, want %q", tc.path, explanation.Decision, tc.decision)
		}
	}
}

func TestLastMatchingRuleWins(t *testing.T) {
	u, _ := newLastRuleWinsUniverse(t)
	prover, err := root.NewProver(u)
	if err != nil {
		t.Fatal(err)
	}
	explanation, err := prover.ExplainDelegation("leaf", root.Tuple{Path: []string{"a"}, Tags: []string{"r"}, Timestamp: 1})
	if err != nil {
		t.Fatal(err)
	}
	if explanation.Decision != root.EffectDeny {
		t.Fatalf("last rule decision = %q, want deny", explanation.Decision)
	}
}

func newWildcardUniverse(t *testing.T) (*root.Universe, testKeySet) {
	t.Helper()
	keys := generateKeys(t, "root", "leaf")
	rootDelegation := root.Delegation{
		ID: "root", Tenant: "tenant-a", Action: "read", SignerKey: "root",
		Rules: []root.Rule{{Pattern: "**", Effect: root.EffectAllow, Start: 0, End: 10}},
	}
	leaf := root.Delegation{
		ID: "leaf", Tenant: "tenant-a", Action: "read", SignerKey: "leaf", QuorumK: 1,
		Parents: []root.ParentRef{{DelegationID: "root"}},
		Rules: []root.Rule{
			{Pattern: "a/*/c/**", Predicate: root.TagPredicate{RequireAll: []string{"r"}}, Effect: root.EffectAllow, Start: 0, End: 10},
		},
	}
	return universeWith(t, []root.Delegation{rootDelegation, leaf}, keys), keys
}

func newLastRuleWinsUniverse(t *testing.T) (*root.Universe, testKeySet) {
	t.Helper()
	keys := generateKeys(t, "root", "leaf")
	rootDelegation := root.Delegation{
		ID: "root", Tenant: "tenant-a", Action: "read", SignerKey: "root",
		Rules: []root.Rule{{Pattern: "**", Effect: root.EffectAllow, Start: 0, End: 10}},
	}
	leaf := root.Delegation{
		ID: "leaf", Tenant: "tenant-a", Action: "read", SignerKey: "leaf", QuorumK: 1,
		Parents: []root.ParentRef{{DelegationID: "root"}},
		Rules: []root.Rule{
			{Pattern: "**", Predicate: root.TagPredicate{RequireAll: []string{"r"}}, Effect: root.EffectAllow, Start: 0, End: 10},
			{Pattern: "**", Predicate: root.TagPredicate{RequireAll: []string{"r"}}, Effect: root.EffectDeny, Start: 0, End: 10},
		},
	}
	return universeWith(t, []root.Delegation{rootDelegation, leaf}, keys), keys
}
