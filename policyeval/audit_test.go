package policyeval_test

import (
	"fmt"
	"testing"

	"example.org/netsec/uid1270/policyeval"
)

func TestRingMappingTerminatesAndIsDetected(t *testing.T) {
	chain := &policyeval.Chain{
		ID: "ring",
		Layers: []policyeval.Layer{
			{
				Name:            "root",
				AllowedPolicies: []policyeval.OID{"1.1.1", "1.1.2"},
			},
			{
				Name:            "mid",
				AllowedPolicies: []policyeval.OID{"1.1.2", "1.1.1"},
				Mappings: []policyeval.Mapping{
					{IssuerDomain: "1.1.1", SubjectDomain: "1.1.2"},
					{IssuerDomain: "1.1.2", SubjectDomain: "1.1.1"},
				},
			},
			{
				Name:            "leaf",
				AllowedPolicies: []policyeval.OID{"1.1.1"},
			},
		},
	}

	result, err := policyeval.EvaluateChain(chain, []policyeval.OID{"1.1.1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Decision != policyeval.DecisionAccepted {
		t.Fatalf("ring chain should still resolve via identity paths: %s", result.Reason)
	}

	// Exhaustive finite enumeration must terminate and agree with the
	// reverse evaluator; the abstract ring must be reported.
	report, err := policyeval.ExhaustiveAudit(
		[]*policyeval.Chain{chain}, []policyeval.OID{"1.1.1"})
	if err != nil {
		t.Fatalf("audit error: %v", err)
	}
	if !report.OK {
		t.Fatalf("single-chain audit should be OK: %+v", report)
	}
	if len(report.Chains[0].Rings) == 0 {
		t.Fatal("expected the 1.1.1 <-> 1.1.2 mapping ring to be detected")
	}
	if !report.Chains[0].MatchesEvaluator {
		t.Fatal("forward enumeration and reverse evaluator disagree on the ring chain")
	}
}

// spliceChains builds two chains that each fail on their own: chain A lacks
// the leaf policy, chain B lacks the root policy. Only an illegal cross-chain
// splice could combine A's root side with B's leaf side.
func spliceChains() []*policyeval.Chain {
	// Both chains share the mid-layer OID midOID, which is what makes a
	// cross-chain splice tempting in the union graph. Chain A can reach the
	// mid layer but cannot produce the requested leaf policy; chain B can
	// produce the leaf policy but has no root policy that reaches its mid
	// layer.
	chainA := &policyeval.Chain{
		ID: "chain-a",
		Layers: []policyeval.Layer{
			{Name: "root", AllowedPolicies: []policyeval.OID{rootOID}},
			{
				Name:            "mid",
				AllowedPolicies: []policyeval.OID{rootOID, midOID},
				Mappings:        []policyeval.Mapping{{IssuerDomain: rootOID, SubjectDomain: midOID}},
			},
			{Name: "leaf", AllowedPolicies: []policyeval.OID{otherOID, midOID}},
		},
	}
	chainB := &policyeval.Chain{
		ID: "chain-b",
		Layers: []policyeval.Layer{
			{Name: "root", AllowedPolicies: []policyeval.OID{rootAlt}},
			{
				Name:            "mid",
				AllowedPolicies: []policyeval.OID{rootAlt, midOID},
			},
			{
				Name:            "leaf",
				AllowedPolicies: []policyeval.OID{leafOID},
				Mappings:        []policyeval.Mapping{{IssuerDomain: midOID, SubjectDomain: leafOID}},
			},
		},
	}
	return []*policyeval.Chain{chainA, chainB}
}

func TestCrossChainsCannotSplice(t *testing.T) {
	chains := spliceChains()

	for _, chain := range chains {
		result, err := policyeval.EvaluateChain(chain, []policyeval.OID{leafOID})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Decision != policyeval.DecisionRejected {
			t.Fatalf("chain %s must fail on its own, got %+v", chain.ID, result)
		}
	}

	ok, accepted, err := policyeval.Authorize(chains, []policyeval.OID{leafOID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok || accepted != nil {
		t.Fatal("two independently failing chains must not authorize together")
	}

	report, err := policyeval.ExhaustiveAudit(chains, []policyeval.OID{leafOID})
	if err != nil {
		t.Fatalf("audit error: %v", err)
	}

	// The union graph must expose the tempting A-root -> B-leaf splice.
	foundSplice := false
	for _, walk := range report.MixedWalks {
		if walk.RootPolicy != rootOID || walk.LeafPolicy != leafOID {
			continue
		}
		foundSplice = true
		contributors := map[string]bool{}
		for _, id := range walk.ChainIDs {
			contributors[id] = true
		}
		if !contributors["chain-a"] || !contributors["chain-b"] {
			t.Fatalf("mixed walk %+v does not actually mix both chains", walk)
		}
	}
	if !foundSplice {
		t.Fatal("expected the audit to enumerate the cross-chain splice walk")
	}
	if !report.SplicePossible || report.OK {
		t.Fatalf("splice must be flagged as possible; report = %+v", report)
	}
}

func TestExhaustiveAuditMatchesEvaluatorOnHappyChain(t *testing.T) {
	report, err := policyeval.ExhaustiveAudit(
		[]*policyeval.Chain{happyChain()}, []policyeval.OID{issuerOID})
	if err != nil {
		t.Fatalf("audit error: %v", err)
	}
	if len(report.Chains) != 1 {
		t.Fatalf("audited chains = %d, want 1", len(report.Chains))
	}
	audit := report.Chains[0]
	if !audit.MatchesEvaluator {
		t.Fatal("forward enumeration and reverse evaluator must derive identical path sets")
	}
	if audit.EnumeratedPaths != 1 {
		t.Fatalf("enumerated paths = %d, want 1", audit.EnumeratedPaths)
	}
	if len(audit.Rings) != 0 {
		t.Fatalf("happy chain has no mapping rings, got %+v", audit.Rings)
	}
	if len(report.MixedWalks) != 0 || report.SplicePossible {
		t.Fatal("a single chain cannot produce mixed walks")
	}
	if !report.OK {
		t.Fatal("happy chain audit should be OK")
	}
}

// TestExhaustiveFiniteSetProperty generates every subset of a small finite
// policy universe for a three-layer chain and cross-checks the reverse
// evaluator against brute-force forward enumeration, including chains with
// inhibit markers, extra mappings and anyPolicy entries.
func TestExhaustiveFiniteSetProperty(t *testing.T) {
	policies := []policyeval.OID{"p0", "p1", "p2", "p3"}

	allSubsets := func() [][]policyeval.OID {
		var subsets [][]policyeval.OID
		for mask := 1; mask < 1<<len(policies); mask++ {
			var subset []policyeval.OID
			for i, policy := range policies {
				if mask&(1<<i) != 0 {
					subset = append(subset, policy)
				}
			}
			subsets = append(subsets, subset)
		}
		return subsets
	}

	subsets := allSubsets()
	caseNo := 0
	for _, rootSet := range subsets {
		for _, midSet := range subsets {
			for _, leafSet := range subsets {
				for _, inhibit := range []bool{false, true} {
					caseNo++
					chain := &policyeval.Chain{
						ID: fmt.Sprintf("prop-%d", caseNo),
						Layers: []policyeval.Layer{
							{Name: "root", AllowedPolicies: rootSet},
							{
								Name:            "mid",
								AllowedPolicies: midSet,
								Mappings: []policyeval.Mapping{
									{IssuerDomain: "p0", SubjectDomain: "p1"},
								},
							},
							{
								Name:            "leaf",
								AllowedPolicies: leafSet,
								InhibitMapping:  inhibit,
								Mappings: []policyeval.Mapping{
									{IssuerDomain: "p1", SubjectDomain: "p2"},
									{IssuerDomain: "p2", SubjectDomain: "p0"},
								},
							},
						},
					}

					leafUsage := leafSet
					report, err := policyeval.ExhaustiveAudit(
						[]*policyeval.Chain{chain}, leafUsage)
					if err != nil {
						t.Fatalf("case %d audit error: %v", caseNo, err)
					}
					if !report.Chains[0].MatchesEvaluator {
						t.Fatalf("case %d: evaluator/enumeration mismatch, chain = %+v",
							caseNo, chain.Layers)
					}

					result, err := policyeval.EvaluateChain(chain, leafUsage)
					if err != nil {
						t.Fatalf("case %d evaluate error: %v", caseNo, err)
					}
					if (result.Decision == policyeval.DecisionAccepted) !=
						(report.Chains[0].EnumeratedPaths > 0) {
						t.Fatalf("case %d: decision %s disagrees with %d enumerated paths",
							caseNo, result.Decision, report.Chains[0].EnumeratedPaths)
					}
				}
			}
		}
	}
}

func TestStructuralValidation(t *testing.T) {
	if _, err := policyeval.EvaluateChain(nil, []policyeval.OID{leafOID}); err == nil {
		t.Fatal("nil chain must error")
	}

	chain := happyChain()
	chain.Layers[0].Mappings = []policyeval.Mapping{
		{IssuerDomain: leafOID, SubjectDomain: otherOID},
	}
	if _, err := policyeval.EvaluateChain(chain, []policyeval.OID{leafOID}); err == nil {
		t.Fatal("trust-anchor layer inbound mappings must be rejected")
	}

	result, err := policyeval.EvaluateChain(happyChain(), []policyeval.OID{"9.9.9"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Decision != policyeval.DecisionRejected || len(result.RootPolicies) != 0 {
		t.Fatalf("leaf usage absent at the leaf layer must reject, got %+v", result)
	}

	if _, err := policyeval.ExhaustiveAudit(nil, []policyeval.OID{leafOID}); err == nil {
		t.Fatal("audit without chains must error")
	}
}
