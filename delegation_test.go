package delegation_test

import (
	"crypto/ed25519"
	"testing"

	root "formal.local/h26-32a6cdd198"
	"formal.local/h26-32a6cdd198/verifier"
)

func TestThreeLayerNarrowingAndStateMachineCertificate(t *testing.T) {
	u, keys := newThreeLayerUniverse(t)
	bundle, counterexample, err := root.Prove(u)
	if err != nil {
		t.Fatalf("root.Prove returned error: %v", err)
	}
	if counterexample != nil {
		t.Fatalf("valid chain returned counterexample: %+v", counterexample)
	}
	if err := verifier.VerifyBundle(u, bundle); err != nil {
		t.Fatalf("independent verification failed: %v", err)
	}
	prover, err := root.NewProver(u)
	if err != nil {
		t.Fatalf("root.NewProver: %v", err)
	}
	explanation, err := prover.Explain(root.Tuple{Path: []string{"a", "b", "c", "a"}, Tags: []string{"r"}, Timestamp: 5})
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if explanation.Decision != root.EffectAllow {
		t.Fatalf("deep wildcard allow = %q, want allow", explanation.Decision)
	}
	denied, err := prover.Explain(root.Tuple{Path: []string{"a", "b", "c", "a"}, Tags: []string{"r"}, Timestamp: 20})
	if err != nil {
		t.Fatalf("Explain denied tuple: %v", err)
	}
	if denied.Decision != root.EffectDeny {
		t.Fatalf("expired window decision = %q, want deny", denied.Decision)
	}
	_ = keys
}

func TestThresholdUnionAndChildDenyCannotBeBypassed(t *testing.T) {
	u, _ := newThresholdUniverse(t)
	bundle, counterexample, err := root.Prove(u)
	if err != nil {
		t.Fatalf("root.Prove returned error: %v", err)
	}
	if counterexample != nil {
		t.Fatalf("threshold graph returned counterexample: %+v", counterexample)
	}
	if err := verifier.VerifyBundle(u, bundle); err != nil {
		t.Fatalf("independent threshold verification failed: %v", err)
	}
	prover, err := root.NewProver(u)
	if err != nil {
		t.Fatalf("root.NewProver: %v", err)
	}
	allowed, err := prover.ExplainDelegation("leaf", root.Tuple{Path: []string{"a", "a"}, Tags: []string{"r"}, Timestamp: 1})
	if err != nil {
		t.Fatalf("Explain threshold tuple: %v", err)
	}
	if allowed.Decision != root.EffectAllow || allowed.QuorumK != 2 || len(allowingVotes(allowed)) != 2 {
		t.Fatalf("quorum explanation = %+v, want two allowing parents", allowed)
	}
	denied, err := prover.ExplainDelegation("leaf", root.Tuple{Path: []string{"b", "a"}, Tags: []string{"r"}, Timestamp: 1})
	if err != nil {
		t.Fatalf("Explain denied threshold tuple: %v", err)
	}
	if denied.Decision != root.EffectDeny {
		t.Fatalf("explicit child deny was bypassed: %+v", denied)
	}
}

func TestTwoOfThreeThresholdUnion(t *testing.T) {
	u, _ := newTwoOfThreeUniverse(t)
	bundle, counterexample, err := root.Prove(u)
	if err != nil || counterexample != nil {
		t.Fatalf("Prove = (%v,%v,%v)", bundle, counterexample, err)
	}
	if err := verifier.VerifyBundle(u, bundle); err != nil {
		t.Fatalf("independent verification failed: %v", err)
	}
	prover, err := root.NewProver(u)
	if err != nil {
		t.Fatal(err)
	}
	explanation, err := prover.ExplainDelegation("leaf", root.Tuple{Path: []string{"a"}, Tags: []string{"r"}, Timestamp: 1})
	if err != nil {
		t.Fatal(err)
	}
	if explanation.Decision != root.EffectAllow || explanation.QuorumK != 2 || len(allowingVotes(explanation)) != 2 {
		t.Fatalf("2-of-3 explanation = %+v, want two allowing parents", explanation)
	}
}

func TestMinimumQuorumCounterexampleAndAttribution(t *testing.T) {
	u, _ := newFailingQuorumUniverse(t)
	_, counterexample, err := root.Prove(u)
	if err != nil {
		t.Fatalf("root.Prove returned error: %v", err)
	}
	if counterexample == nil {
		t.Fatal("expected unclosed quorum counterexample")
	}
	wantPath := []string{"a"}
	wantTags := []string{"r"}
	if !equalStrings(counterexample.Path, wantPath) || !equalStrings(counterexample.Tags, wantTags) || counterexample.Timestamp != 0 {
		t.Fatalf("counterexample = (%v,%v,%d), want (%v,%v,0)", counterexample.Path, counterexample.Tags, counterexample.Timestamp, wantPath, wantTags)
	}
	if counterexample.FailedQuorum == nil || counterexample.FailedQuorum.DelegationID != "leaf" ||
		counterexample.FailedQuorum.Required != 2 || counterexample.FailedQuorum.Achieved != 1 {
		t.Fatalf("quorum attribution does not match minimum counterexample: %+v", counterexample)
	}
	if counterexample.FailedParent != nil {
		t.Fatalf("quorum failure incorrectly reported as parent failure: %+v", counterexample.FailedParent)
	}
}

func TestFailedParentAttributionMatchesMinimumCounterexample(t *testing.T) {
	u, _ := newFailingParentUniverse(t)
	_, counterexample, err := root.Prove(u)
	if err != nil {
		t.Fatalf("Prove returned error: %v", err)
	}
	if counterexample == nil || counterexample.FailedParent == nil {
		t.Fatalf("expected parent attribution, got %+v", counterexample)
	}
	if len(counterexample.Path) != 1 || counterexample.Path[0] != "a" ||
		!equalStrings(counterexample.Tags, []string{"r"}) || counterexample.Timestamp != -1<<63 {
		t.Fatalf("parent counterexample is not the minimum tuple: %+v", counterexample)
	}
	if counterexample.FailedQuorum != nil || counterexample.FailedParent == nil {
		t.Fatalf("expected parent attribution after leaf evaluation, got %+v", counterexample)
	}
	if counterexample.FailedParent.DelegationID != "leaf" ||
		counterexample.FailedParent.ParentID != "mid" ||
		counterexample.FailedQuorum != nil {
		t.Fatalf("parent attribution mismatch: %+v", counterexample)
	}
}

func TestLabelConstraints(t *testing.T) {
	u, _ := newThreeLayerUniverse(t)
	prover, err := root.NewProver(u)
	if err != nil {
		t.Fatalf("root.NewProver: %v", err)
	}
	if _, err := prover.Explain(root.Tuple{Path: []string{"a"}, Tags: []string{"r", "x", "z"}, Timestamp: 1}); err == nil {
		t.Fatal("mutex tags were accepted")
	}
	if _, err := prover.Explain(root.Tuple{Path: []string{"a"}, Tags: []string{"x", "y"}, Timestamp: 1}); err == nil {
		t.Fatal("missing required tag was accepted")
	}
	if _, err := prover.Explain(root.Tuple{Path: []string{"a"}, Tags: []string{"r", "y"}, Timestamp: 1}); err == nil {
		t.Fatal("implication was not enforced")
	}
}

func TestImplicationAndForbiddenPredicate(t *testing.T) {
	u, _ := newImplicationUniverse(t)
	prover, err := root.NewProver(u)
	if err != nil {
		t.Fatal(err)
	}
	explanation, err := prover.ExplainDelegation("leaf", root.Tuple{
		Path:      []string{"a"},
		Tags:      []string{"r", "y", "z"},
		Timestamp: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if explanation.Decision != root.EffectDeny {
		t.Fatalf("implied forbidden label did not block rule: %+v", explanation)
	}
}

func TestTamperedCertificateFailsIndependentVerification(t *testing.T) {
	u, _ := newThreeLayerUniverse(t)
	bundle, counterexample, err := root.Prove(u)
	if err != nil || counterexample != nil {
		t.Fatalf("root.Prove = (%v,%v,%v)", bundle, counterexample, err)
	}
	bundle.Certificates["leaf"].Outputs[0].Decision = root.EffectAllow
	if err := verifier.VerifyBundle(u, bundle); err == nil {
		t.Fatal("tampered certificate passed independent verification")
	}
}

func newThreeLayerUniverse(t *testing.T) (*root.Universe, testKeySet) {
	t.Helper()
	keys := generateKeys(t, "root", "mid", "leaf")
	rootDelegation := root.Delegation{
		ID: "root", Tenant: "tenant-a", Action: "read", SignerKey: "root",
		Rules: []root.Rule{{Pattern: "**", Predicate: root.TagPredicate{}, Effect: root.EffectAllow, Start: 0, End: 30}},
	}
	mid := root.Delegation{
		ID: "mid", Tenant: "tenant-a", Action: "read", SignerKey: "mid", QuorumK: 1,
		Parents: []root.ParentRef{{DelegationID: "root"}},
		Rules: []root.Rule{
			{Pattern: "a/*/**", Predicate: root.TagPredicate{RequireAll: []string{"r"}}, Effect: root.EffectAllow, Start: 0, End: 10},
			{Pattern: "a/*/**", Predicate: root.TagPredicate{RequireAll: []string{"r"}}, Effect: root.EffectAllow, Start: 10, End: 20},
		},
	}
	leaf := root.Delegation{
		ID: "leaf", Tenant: "tenant-a", Action: "read", SignerKey: "leaf", QuorumK: 1,
		Parents: []root.ParentRef{{DelegationID: "mid"}},
		Rules:   []root.Rule{{Pattern: "a/b/**", Predicate: root.TagPredicate{RequireAll: []string{"r"}}, Effect: root.EffectAllow, Start: 0, End: 10}},
	}
	return universeWith(t, []root.Delegation{rootDelegation, mid, leaf}, keys), keys
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

func newTwoOfThreeUniverse(t *testing.T) (*root.Universe, testKeySet) {
	t.Helper()
	keys := generateKeys(t, "root", "p1", "p2", "p3", "leaf")
	rootDelegation := root.Delegation{
		ID: "root", Tenant: "tenant-a", Action: "read", SignerKey: "root",
		Rules: []root.Rule{{Pattern: "**", Predicate: root.TagPredicate{}, Effect: root.EffectAllow, Start: 0, End: 10}},
	}
	parent := func(id string, pattern string, effect root.Effect) root.Delegation {
		return root.Delegation{
			ID: id, Tenant: "tenant-a", Action: "read", SignerKey: id, QuorumK: 1,
			Parents: []root.ParentRef{{DelegationID: "root"}},
			Rules: []root.Rule{{
				Pattern: pattern, Predicate: root.TagPredicate{RequireAll: []string{"r"}},
				Effect: effect, Start: 0, End: 10,
			}},
		}
	}
	leaf := root.Delegation{
		ID: "leaf", Tenant: "tenant-a", Action: "read", SignerKey: "leaf", QuorumK: 2,
		Parents: []root.ParentRef{{DelegationID: "p1"}, {DelegationID: "p2"}, {DelegationID: "p3"}},
		Rules:   []root.Rule{{Pattern: "a/**", Predicate: root.TagPredicate{RequireAll: []string{"r"}}, Effect: root.EffectAllow, Start: 0, End: 10}},
	}
	return universeWith(t, []root.Delegation{
		rootDelegation,
		parent("p1", "a/**", root.EffectAllow),
		parent("p2", "a/**", root.EffectAllow),
		parent("p3", "b/**", root.EffectDeny),
		leaf,
	}, keys), keys
}

func newFailingQuorumUniverse(t *testing.T) (*root.Universe, testKeySet) {
	t.Helper()
	keys := generateKeys(t, "root", "mid", "leaf")
	rootDelegation := root.Delegation{
		ID: "root", Tenant: "tenant-a", Action: "read", SignerKey: "root",
		Rules: []root.Rule{{Pattern: "**", Predicate: root.TagPredicate{}, Effect: root.EffectAllow, Start: 0, End: 10}},
	}
	mid := root.Delegation{
		ID: "mid", Tenant: "tenant-a", Action: "read", SignerKey: "mid", QuorumK: 1,
		Parents: []root.ParentRef{{DelegationID: "root"}},
		Rules:   []root.Rule{{Pattern: "c/**", Predicate: root.TagPredicate{RequireAll: []string{"r"}}, Effect: root.EffectAllow, Start: 0, End: 10}},
	}
	leaf := root.Delegation{
		ID: "leaf", Tenant: "tenant-a", Action: "read", SignerKey: "leaf", QuorumK: 2,
		Parents: []root.ParentRef{{DelegationID: "root"}, {DelegationID: "mid"}},
		Rules:   []root.Rule{{Pattern: "**", Predicate: root.TagPredicate{RequireAll: []string{"r"}}, Effect: root.EffectAllow, Start: 0, End: 10}},
	}
	return universeWith(t, []root.Delegation{rootDelegation, mid, leaf}, keys), keys
}

func newFailingParentUniverse(t *testing.T) (*root.Universe, testKeySet) {
	t.Helper()
	keys := generateKeys(t, "root", "other", "mid", "leaf")
	rootDelegation := root.Delegation{
		ID: "root", Tenant: "tenant-a", Action: "read", SignerKey: "root",
		Rules: []root.Rule{{Pattern: "**", Predicate: root.TagPredicate{}, Effect: root.EffectAllow, Start: 0, End: 10}},
	}
	other := root.Delegation{
		ID: "other", Tenant: "tenant-a", Action: "read", SignerKey: "other",
		QuorumK: 1, Parents: []root.ParentRef{{DelegationID: "root"}},
		Rules: []root.Rule{{Pattern: "c/**", Predicate: root.TagPredicate{RequireAll: []string{"r"}}, Effect: root.EffectAllow, Start: 0, End: 10}},
	}
	mid := root.Delegation{
		ID: "mid", Tenant: "tenant-a", Action: "read", SignerKey: "mid", QuorumK: 2,
		Parents: []root.ParentRef{{DelegationID: "root"}, {DelegationID: "other"}},
		Rules:   []root.Rule{{Pattern: "**", Predicate: root.TagPredicate{RequireAll: []string{"r"}}, Effect: root.EffectAllow, Start: 0, End: 10}},
	}
	leaf := root.Delegation{
		ID: "leaf", Tenant: "tenant-a", Action: "read", SignerKey: "leaf", QuorumK: 1,
		Parents: []root.ParentRef{{DelegationID: "mid"}},
		Rules:   []root.Rule{{Pattern: "**", Predicate: root.TagPredicate{RequireAll: []string{"r"}}, Effect: root.EffectAllow, Start: 0, End: 10}},
	}
	return universeWith(t, []root.Delegation{rootDelegation, other, mid, leaf}, keys), keys
}

func newImplicationUniverse(t *testing.T) (*root.Universe, testKeySet) {
	t.Helper()
	keys := generateKeys(t, "root", "leaf")
	rootDelegation := root.Delegation{
		ID: "root", Tenant: "tenant-a", Action: "read", SignerKey: "root",
		Rules: []root.Rule{{Pattern: "**", Effect: root.EffectAllow, Start: 0, End: 10}},
	}
	leaf := root.Delegation{
		ID: "leaf", Tenant: "tenant-a", Action: "read", SignerKey: "leaf", QuorumK: 1,
		Parents: []root.ParentRef{{DelegationID: "root"}},
		Rules: []root.Rule{{
			Pattern:   "**",
			Predicate: root.TagPredicate{RequireAll: []string{"r"}, ForbidAll: []string{"z"}},
			Effect:    root.EffectAllow,
			Start:     0,
			End:       10,
		}},
	}
	u := universeWith(t, []root.Delegation{rootDelegation, leaf}, keys)
	u.LabelRules.Implications = []root.Implication{{IfLabel: "y", ThenLabel: "z"}}
	return reSignUniverse(t, u, keys), keys
}

func reSignUniverse(t *testing.T, u *root.Universe, keys testKeySet) *root.Universe {
	t.Helper()
	for i, signed := range u.Delegations {
		resigned, err := root.SignDelegation(signed.Delegation, keys.private[signed.Delegation.SignerKey])
		if err != nil {
			t.Fatal(err)
		}
		u.Delegations[i] = resigned
	}
	return u
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
		Labels:           []string{"r", "x", "y", "z"},
		Delegations:      signed,
		KeyRing:          keys.public,
		RootDelegationID: "root",
		LabelRules: root.LabelConstraints{
			Required:     []string{"r"},
			MutexPairs:   [][2]string{{"x", "z"}},
			Implications: []root.Implication{{IfLabel: "y", ThenLabel: "z"}},
		},
	}
}

func allowingVotes(explanation *root.Explanation) []string {
	var allowing []string
	for id, vote := range explanation.ParentVotes {
		if vote == root.EffectAllow {
			allowing = append(allowing, id)
		}
	}
	return allowing
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
