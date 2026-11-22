package redemption

import "testing"

// TestWorstCaseFullTree builds 22 independent zero-entitlement transactions
// so no value pruning fires: the search must cover the full 2^22 leaves and
// the certificate stays complete enough for independent verification.
func TestWorstCaseFullTree(t *testing.T) {
	p := Problem{
		Issuers: []IssuerID{issuerA, issuerB},
		Quotas:  []Quota{{Issuer: issuerA, Activity: act1, Limit: 1 << 30}},
	}
	for i := 0; i < 22; i++ {
		id := TxID("z" + itoa(i))
		// Pure change transactions: zero service cost, zero entitlements.
		p.Candidates = append(p.Candidates, Transaction{
			ID:      id,
			Inputs:  []Input{originInput(id, 0, issuerA, act1, 1, Nullifier("nf-"+itoa(i)))},
			Outputs: []ChangeOutput{changeOut(issuerA, act1, 1, string(id))},
		})
	}
	sol, err := Solve(p, acceptAll, acceptAllProof)
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	if err := VerifySolution(p, sol, acceptAll, acceptAllProof); err != nil {
		t.Fatalf("certificate: %v", err)
	}
	if sol.Entitlements != 0 {
		t.Fatalf("want zero value, got %d (%v)", sol.Entitlements, sol.Selected)
	}
	// All zero-cost transactions are mutually independent, so the
	// lexicographic tie-break selects the full set.
	if len(sol.Selected) != 22 {
		t.Fatalf("want full zero-value set, got %d of 22", len(sol.Selected))
	}
	t.Logf("worst-case nodes=%d (full tree %d)",
		len(sol.Certificate.Nodes), 1<<23-1)
}
