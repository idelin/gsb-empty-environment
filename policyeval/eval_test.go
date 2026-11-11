package policyeval_test

import (
	"testing"

	"example.org/netsec/uid1270/policyeval"
)

const (
	rootOID   policyeval.OID = "1.2.3.1"
	rootAlt   policyeval.OID = "1.2.3.9"
	midOID    policyeval.OID = "1.2.3.2"
	midAlt    policyeval.OID = "1.2.3.8"
	issuerOID policyeval.OID = "1.2.3.3"
	leafOID   policyeval.OID = "1.2.3.4"
	otherOID  policyeval.OID = "1.2.3.7"
)

// happyChain is a four-layer chain with two declared mappings. Each mapping
// is carried by its subject-side layer and translates an issuer-domain OID
// (admissible at the layer above) into a subject-domain OID used below:
//
//	root layer:   {rootOID}
//	mid layer:    {rootOID, midOID}    maps rootOID->midOID
//	issuer layer: {midOID, issuerOID}  maps midOID->issuerOID
//	leaf layer:   {issuerOID}
func happyChain() *policyeval.Chain {
	return &policyeval.Chain{
		ID: "happy",
		Layers: []policyeval.Layer{
			{
				Name:            "root",
				AllowedPolicies: []policyeval.OID{rootOID, rootAlt},
			},
			{
				Name:            "mid",
				AllowedPolicies: []policyeval.OID{rootOID, midOID, midAlt},
				Mappings: []policyeval.Mapping{
					{IssuerDomain: rootOID, SubjectDomain: midOID},
				},
			},
			{
				Name:            "issuer",
				AllowedPolicies: []policyeval.OID{midOID, issuerOID},
				Mappings: []policyeval.Mapping{
					{IssuerDomain: midOID, SubjectDomain: issuerOID},
				},
			},
			{
				Name:            "leaf",
				AllowedPolicies: []policyeval.OID{issuerOID},
			},
		},
	}
}

func TestEvaluateChainAcceptedMappingPath(t *testing.T) {
	result, err := policyeval.EvaluateChain(happyChain(), []policyeval.OID{issuerOID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Decision != policyeval.DecisionAccepted {
		t.Fatalf("decision = %q, reason = %q", result.Decision, result.Reason)
	}

	if len(result.RootPolicies) != 1 || result.RootPolicies[0] != rootOID {
		t.Fatalf("root policies = %v, want [%s]", result.RootPolicies, rootOID)
	}
	if len(result.Paths) != 1 {
		t.Fatalf("paths = %d, want exactly one complete mapping path", len(result.Paths))
	}

	path := result.Paths[0]
	if path.RootPolicy != rootOID || path.LeafPolicy != issuerOID {
		t.Fatalf("path brackets = %s -> %s", path.RootPolicy, path.LeafPolicy)
	}
	if len(path.Steps) != 4 {
		t.Fatalf("path steps = %d, want 4", len(path.Steps))
	}

	wantPolicies := []policyeval.OID{rootOID, midOID, issuerOID, issuerOID}
	for i, want := range wantPolicies {
		if path.Steps[i].Layer != i || path.Steps[i].Policy != want {
			t.Fatalf("step %d = layer %d policy %s, want layer %d policy %s",
				i, path.Steps[i].Layer, path.Steps[i].Policy, i, want)
		}
	}
	if path.Steps[0].AppliedMapping != nil {
		t.Fatal("trust-anchor step must carry no inbound mapping")
	}
	if path.Steps[1].AppliedMapping == nil ||
		path.Steps[1].AppliedMapping.IssuerDomain != rootOID ||
		path.Steps[1].AppliedMapping.SubjectDomain != midOID {
		t.Fatalf("first boundary mapping = %+v, want root->mid", path.Steps[1].AppliedMapping)
	}
	if path.Steps[2].AppliedMapping == nil ||
		path.Steps[2].AppliedMapping.IssuerDomain != midOID ||
		path.Steps[2].AppliedMapping.SubjectDomain != issuerOID {
		t.Fatalf("second boundary mapping = %+v, want mid->issuer", path.Steps[2].AppliedMapping)
	}
	if path.Steps[3].AppliedMapping != nil {
		t.Fatalf("issuer->leaf must use identity, got mapping %+v", path.Steps[3].AppliedMapping)
	}
}

func TestEvaluateChainInhibitMappingRejectsAndAllowsIdentity(t *testing.T) {
	chain := happyChain()
	// Inhibiting the mid layer's upper boundary kills the root->mid mapping.
	chain.Layers[1].InhibitMapping = true

	result, err := policyeval.EvaluateChain(chain, []policyeval.OID{issuerOID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Decision != policyeval.DecisionRejected {
		t.Fatalf("decision = %q, want rejected", result.Decision)
	}
	if len(result.RootPolicies) != 0 || len(result.Paths) != 0 {
		t.Fatal("rejected chain must return empty root set and paths")
	}

	// A separate chain where the inhibited boundary is crossed by the
	// identity relation: the same OID is explicitly admitted on both sides.
	identityChain := &policyeval.Chain{
		ID: "inhibit-identity",
		Layers: []policyeval.Layer{
			{Name: "root", AllowedPolicies: []policyeval.OID{rootOID}},
			{
				Name:            "mid",
				AllowedPolicies: []policyeval.OID{rootOID},
				InhibitMapping:  true,
				Mappings: []policyeval.Mapping{
					{IssuerDomain: rootOID, SubjectDomain: otherOID},
				},
			},
			{Name: "leaf", AllowedPolicies: []policyeval.OID{rootOID}},
		},
	}

	result, err = policyeval.EvaluateChain(identityChain, []policyeval.OID{rootOID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Decision != policyeval.DecisionAccepted {
		t.Fatalf("identity across inhibited boundary failed: %s", result.Reason)
	}
	if len(result.Paths) != 1 || result.Paths[0].RootPolicy != rootOID {
		t.Fatalf("want one rootOID-rooted identity path, got %+v", result.Paths)
	}
	if !result.Paths[0].Steps[1].Inhibited {
		t.Fatal("inhibited boundary must be flagged on the subject-side step")
	}
	if result.Paths[0].Steps[1].AppliedMapping != nil {
		t.Fatal("inhibited boundary must cross via identity, not a mapping")
	}
}

func TestEmptySetRejects(t *testing.T) {
	// A layer whose admissible set empties during derivation rejects.
	chain := happyChain()
	chain.Layers[2].AllowedPolicies = []policyeval.OID{otherOID}
	chain.Layers[2].Mappings = nil

	result, err := policyeval.EvaluateChain(chain, []policyeval.OID{issuerOID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Decision != policyeval.DecisionRejected || len(result.RootPolicies) != 0 {
		t.Fatalf("want rejected with empty root set, got %+v", result)
	}

	// An empty leaf usage set is a caller error rather than a silent allow.
	if _, err := policyeval.EvaluateChain(happyChain(), nil); err == nil {
		t.Fatal("empty leaf usage must return an error, never default-allow")
	}

	// A structurally empty allowed-policy layer is likewise a caller error.
	bad := happyChain()
	bad.Layers[0].AllowedPolicies = nil
	if _, err := policyeval.EvaluateChain(bad, []policyeval.OID{issuerOID}); err == nil {
		t.Fatal("empty allowed-policy layer must return an error")
	}
}

func TestMappingOnlyAtDeclaredAdjacentBoundary(t *testing.T) {
	// The root->mid mapping is only valid at the root/mid boundary. A
	// mapping placed on the trust-anchor layer has no upper boundary, so it
	// is rejected structurally rather than silently applied elsewhere.
	chain := happyChain()
	chain.Layers[0].Mappings = []policyeval.Mapping{
		{IssuerDomain: rootOID, SubjectDomain: midOID},
	}

	if _, err := policyeval.EvaluateChain(chain, []policyeval.OID{issuerOID}); err == nil {
		t.Fatal("a mapping on the trust-anchor layer must be rejected")
	}

	// Moving the root->mid mapping one boundary down (onto the issuer
	// layer) makes it a mid/issuer relation, so it can no longer bridge the
	// root/mid boundary and the leaf usage cannot be derived.
	chain = happyChain()
	chain.Layers[1].Mappings = nil
	chain.Layers[2].Mappings = append(chain.Layers[2].Mappings,
		policyeval.Mapping{IssuerDomain: rootOID, SubjectDomain: midOID})
	result, err := policyeval.EvaluateChain(chain, []policyeval.OID{issuerOID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Decision != policyeval.DecisionRejected {
		t.Fatalf("a mapping moved off its declared boundary must not be reused, got %+v", result)
	}

	// A mapping whose endpoints are not both allowed at their own layers is
	// inert.
	chain = happyChain()
	chain.Layers[1].Mappings = []policyeval.Mapping{
		{IssuerDomain: rootAlt, SubjectDomain: otherOID},
	}
	result, err = policyeval.EvaluateChain(chain, []policyeval.OID{issuerOID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Decision != policyeval.DecisionRejected {
		t.Fatalf("mapping with disallowed endpoints must be ignored, got %+v", result)
	}
}

func TestAnyPolicyWildcard(t *testing.T) {
	chain := &policyeval.Chain{
		ID: "wild",
		Layers: []policyeval.Layer{
			{
				Name:            "root",
				AllowedPolicies: []policyeval.OID{policyeval.AnyPolicy},
			},
			{
				Name:            "leaf",
				AllowedPolicies: []policyeval.OID{leafOID},
			},
		},
	}

	result, err := policyeval.EvaluateChain(chain, []policyeval.OID{leafOID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Decision != policyeval.DecisionAccepted {
		t.Fatalf("anyPolicy root must accept the concrete leaf usage: %s", result.Reason)
	}
	if len(result.RootPolicies) != 1 || result.RootPolicies[0] != leafOID {
		t.Fatalf("root policy = %v, want the concrete leaf OID via identity", result.RootPolicies)
	}
}

func TestAuthorizeRequiresAnAcceptingChain(t *testing.T) {
	ok, accepted, err := policyeval.Authorize(
		[]*policyeval.Chain{happyChain()}, []policyeval.OID{issuerOID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok || len(accepted) != 1 || accepted[0].ChainID != "happy" {
		t.Fatalf("authorize = %v, %+v; want one accepted happy chain", ok, accepted)
	}

	failing := happyChain()
	failing.Layers[2].AllowedPolicies = []policyeval.OID{otherOID}
	failing.Layers[2].Mappings = nil
	ok, accepted, err = policyeval.Authorize(
		[]*policyeval.Chain{failing}, []policyeval.OID{issuerOID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok || accepted != nil {
		t.Fatalf("authorize must fail when every candidate chain rejects")
	}
}
