package redemption

import "testing"

// TestPerKeyQuota: quotas are scoped per (issuer, activity). Generous quota
// on one key must not subsidize an over-limit redemption on another key.
func TestPerKeyQuota(t *testing.T) {
	p := Problem{
		Issuers: []IssuerID{issuerA, issuerB},
		Quotas: []Quota{
			{Issuer: issuerA, Activity: act1, Limit: 100},
			{Issuer: issuerB, Activity: act1, Limit: 2},
			{Issuer: issuerA, Activity: act2, Limit: 1},
		},
		Candidates: []Transaction{
			{
				ID:     "b-over",
				Inputs: []Input{originInput("b-over", 0, issuerB, act1, 10, "nf-b")},
				Spends: []ServiceEntitlement{service("svc", issuerB, act1, 5, 10)},
			},
			{
				ID:     "a2-over",
				Inputs: []Input{originInput("a2-over", 0, issuerA, act2, 4, "nf-a2")},
				Spends: []ServiceEntitlement{service("svc", issuerA, act2, 2, 4)},
			},
			{
				ID:     "a1-ok",
				Inputs: []Input{originInput("a1-ok", 0, issuerA, act1, 3, "nf-a1")},
				Spends: []ServiceEntitlement{service("svc", issuerA, act1, 3, 3)},
			},
		},
	}
	ledger, err := Prepare(p, acceptAll, acceptAllProof)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	for _, id := range []TxID{"b-over", "a2-over"} {
		m, _ := ledger.MaskFromIDs([]TxID{id})
		if ok, _ := ledger.Feasible(m); ok {
			t.Fatalf("%s accepted despite over-limit key", id)
		}
	}
	bestMask, bestValue := ledger.BruteForce()
	if bestValue != 3 || !equalIDs(ledger.SelectedIDs(bestMask), []TxID{"a1-ok"}) {
		t.Fatalf("unexpected optimum value=%d ids=%v", bestValue, ledger.SelectedIDs(bestMask))
	}
	sol, err := Solve(p, acceptAll, acceptAllProof)
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	if err := VerifySolution(p, sol, acceptAll, acceptAllProof); err != nil {
		t.Fatalf("certificate: %v", err)
	}
}

// TestEmptyProblem: with no candidates the unique optimum is the empty set
// and the certificate verifies.
func TestEmptyProblem(t *testing.T) {
	p := Problem{Issuers: []IssuerID{issuerA, issuerB}}
	sol, err := Solve(p, acceptAll, acceptAllProof)
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	if sol.Entitlements != 0 || len(sol.Selected) != 0 {
		t.Fatalf("want empty solution, got %+v", sol)
	}
	if err := VerifySolution(p, sol, acceptAll, acceptAllProof); err != nil {
		t.Fatalf("certificate: %v", err)
	}
}

// TestDigestTamperDetection mutates one semantic field and requires the
// certificate binding to fail.
func TestDigestTamperDetection(t *testing.T) {
	p := Problem{
		Issuers: []IssuerID{issuerA, issuerB},
		Quotas:  []Quota{{Issuer: issuerA, Activity: act1, Limit: 10}},
		Candidates: []Transaction{
			{
				ID:     "tx",
				Inputs: []Input{originInput("tx", 0, issuerA, act1, 3, "nf")},
				Spends: []ServiceEntitlement{service("svc", issuerA, act1, 1, 3)},
			},
		},
	}
	sol, err := Solve(p, acceptAll, acceptAllProof)
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	tampered := p
	tampered.Quotas[0].Limit = 99
	if err := VerifySolution(tampered, sol, acceptAll, acceptAllProof); err == nil {
		t.Fatal("tampered problem accepted against stale certificate")
	}
}

// TestDependencyClosure: selecting a child without its change-creating
// predecessor is rejected even when no nullifier is shared.
func TestDependencyClosure(t *testing.T) {
	p := Problem{
		Issuers: []IssuerID{issuerA, issuerB},
		Quotas:  []Quota{{Issuer: issuerA, Activity: act1, Limit: 100}},
		Candidates: []Transaction{
			{
				ID:      "parent",
				Inputs:  []Input{originInput("parent", 0, issuerA, act1, 5, "nf-p")},
				Outputs: []ChangeOutput{changeOut(issuerA, act1, 5, "parent")},
			},
			{
				ID:     "child",
				Inputs: []Input{changeInput("parent", 0, issuerA, act1, 5, "nf-c", "change/parent")},
				Spends: []ServiceEntitlement{service("svc", issuerA, act1, 2, 5)},
			},
		},
	}
	ledger, err := Prepare(p, acceptAll, acceptAllProof)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	childOnly, _ := ledger.MaskFromIDs([]TxID{"child"})
	if ok, _ := ledger.Feasible(childOnly); ok {
		t.Fatal("child accepted without its predecessor")
	}
	sol, err := Solve(p, acceptAll, acceptAllProof)
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	if !equalIDs(sol.Selected, []TxID{"child", "parent"}) {
		t.Fatalf("want both parent and child, got %v", sol.Selected)
	}
	if err := VerifySolution(p, sol, acceptAll, acceptAllProof); err != nil {
		t.Fatalf("certificate: %v", err)
	}
}
