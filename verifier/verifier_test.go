package verifier_test

import (
	"crypto/ed25519"
	"testing"

	root "formal.local/h26-32a6cdd198"
	"formal.local/h26-32a6cdd198/verifier"
)

func TestVerifyIgnoresUnreachableDelegation(t *testing.T) {
	u, _ := newThresholdUniverse(t)
	isolatedPub, isolatedPriv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	isolated, err := root.SignDelegation(root.Delegation{
		ID: "isolated", Tenant: "tenant-a", Action: "read", SignerKey: "isolated",
		Rules: []root.Rule{{Pattern: "**", Effect: root.EffectAllow, Start: 0, End: 1}},
	}, isolatedPriv)
	if err != nil {
		t.Fatal(err)
	}
	u.Delegations = append(u.Delegations, isolated)
	u.KeyRing["isolated"] = isolatedPub
	bundle, counterexample, err := root.Prove(u)
	if err != nil || counterexample != nil {
		t.Fatalf("Prove = (%v,%v,%v)", bundle, counterexample, err)
	}
	if err := verifier.VerifyBundle(u, bundle); err != nil {
		t.Fatalf("valid bundle failed: %v", err)
	}
}

func TestVerifyRejectsForgedQuorumAttribution(t *testing.T) {
	u, _ := newThresholdUniverse(t)
	bundle, _, err := root.Prove(u)
	if err != nil {
		t.Fatal(err)
	}
	for _, cert := range bundle.Certificates {
		for i := range cert.Outputs {
			if cert.Outputs[i].Decision == root.EffectDeny {
				cert.Outputs[i].Achieved = cert.QuorumK
			}
		}
	}
	if err := verifier.VerifyBundle(u, bundle); err == nil {
		t.Fatal("forged quorum attribution passed verification")
	}
}

func TestVerifyRejectsForgedPathTransition(t *testing.T) {
	u, _ := newThresholdUniverse(t)
	bundle, _, err := root.Prove(u)
	if err != nil {
		t.Fatal(err)
	}
	cert := bundle.Certificates["leaf"]
	if len(cert.PathStates) < 2 {
		t.Fatal("expected multiple path states")
	}
	cert.PathStates[0].Transition[0] = 0
	if err := verifier.VerifyBundle(u, bundle); err == nil {
		t.Fatal("forged path transition passed verification")
	}
}

func newThresholdUniverse(t *testing.T) (*root.Universe, testKeySet) {
	t.Helper()
	keys := generateKeys(t, "root", "mid", "leaf")
	rootDelegation := root.Delegation{
		ID: "root", Tenant: "tenant-a", Action: "read", SignerKey: "root",
		Rules: []root.Rule{{Pattern: "**", Predicate: root.TagPredicate{}, Effect: root.EffectAllow, Start: 0, End: 10}},
	}
	mid := root.Delegation{
		ID: "mid", Tenant: "tenant-a", Action: "read", SignerKey: "mid", QuorumK: 1,
		Parents: []root.ParentRef{{DelegationID: "root"}},
		Rules: []root.Rule{
			{Pattern: "a/**", Predicate: root.TagPredicate{RequireAll: []string{"r"}}, Effect: root.EffectAllow, Start: 0, End: 10},
			{Pattern: "b/**", Predicate: root.TagPredicate{RequireAll: []string{"r"}}, Effect: root.EffectDeny, Start: 0, End: 10},
		},
	}
	leaf := root.Delegation{
		ID: "leaf", Tenant: "tenant-a", Action: "read", SignerKey: "leaf", QuorumK: 2,
		Parents: []root.ParentRef{{DelegationID: "root"}, {DelegationID: "mid"}},
		Rules: []root.Rule{
			{Pattern: "a/**", Predicate: root.TagPredicate{RequireAll: []string{"r"}}, Effect: root.EffectAllow, Start: 0, End: 10},
			{Pattern: "b/**", Predicate: root.TagPredicate{RequireAll: []string{"r"}}, Effect: root.EffectDeny, Start: 0, End: 10},
		},
	}
	return universeWith(t, []root.Delegation{rootDelegation, mid, leaf}, keys), keys
}

type testKeySet struct {
	public  map[string]ed25519.PublicKey
	private map[string]ed25519.PrivateKey
}

func generateKeys(t *testing.T, ids ...string) testKeySet {
	t.Helper()
	keys := testKeySet{
		public:  map[string]ed25519.PublicKey{},
		private: map[string]ed25519.PrivateKey{},
	}
	for _, id := range ids {
		pub, priv, err := ed25519.GenerateKey(nil)
		if err != nil {
			t.Fatal(err)
		}
		keys.public[id] = pub
		keys.private[id] = priv
	}
	return keys
}

func universeWith(t *testing.T, delegations []root.Delegation, keys testKeySet) *root.Universe {
	t.Helper()
	signed := make([]root.SignedDelegation, 0, len(delegations))
	for _, d := range delegations {
		sd, err := root.SignDelegation(d, keys.private[d.SignerKey])
		if err != nil {
			t.Fatal(err)
		}
		signed = append(signed, sd)
	}
	return &root.Universe{
		Tenant:           "tenant-a",
		Action:           "read",
		PathAlphabet:     []string{"a", "b", "c"},
		Labels:           []string{"r"},
		Delegations:      signed,
		KeyRing:          keys.public,
		RootDelegationID: "root",
		LabelRules:       root.LabelConstraints{Required: []string{"r"}},
	}
}
