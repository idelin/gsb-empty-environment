package redemption

import "testing"

// TestZeroCostCycle builds three zero-service-cost transactions that form a
// closed change loop (cyc-x -> cyc-y -> cyc-close -> cyc-x). Every member is
// individually well-formed and conserves value, but together they self-fund
// and must be rejected. An independently rooted service transaction remains
// acceptable and wins.
func TestZeroCostCycle(t *testing.T) {
	const v int64 = 8

	// cyc-x: one issuer note (v) plus the closing change note (v); emits 2v
	// change consumed by cyc-y.
	cycX := Transaction{
		ID: "cyc-x",
		Inputs: []Input{
			originInput("cyc-x", 0, issuerA, act1, v, "nf-x-origin"),
			changeInput("cyc-close", 0, issuerA, act1, v, "nf-x-back", "change/cyc-close"),
		},
		Outputs: []ChangeOutput{changeOut(issuerA, act1, 2*v, "cyc-x")},
	}
	// cyc-y: consumes cyc-x (2v); emits 2v change consumed by cyc-close.
	cycY := Transaction{
		ID:      "cyc-y",
		Inputs:  []Input{changeInput("cyc-x", 0, issuerA, act1, 2*v, "nf-y-in", "change/cyc-x")},
		Outputs: []ChangeOutput{changeOut(issuerA, act1, 2*v, "cyc-y")},
	}
	// cyc-close: consumes cyc-y (2v); emits two v change notes, index 0 loops
	// back into cyc-x and index 1 stays unspent.
	cycClose := Transaction{
		ID:     "cyc-close",
		Inputs: []Input{changeInput("cyc-y", 0, issuerA, act1, 2*v, "nf-close-in", "change/cyc-y")},
		Outputs: []ChangeOutput{
			changeOut(issuerA, act1, v, "cyc-close"),
			changeOut(issuerA, act1, v, "cyc-close-extra"),
		},
	}
	rooted := Transaction{
		ID:     "rooted-service",
		Inputs: []Input{originInput("rooted-service", 0, issuerA, act1, 4, "nf-rooted")},
		Spends: []ServiceEntitlement{service("svc", issuerA, act1, 2, 4)},
	}

	p := Problem{
		Issuers:    []IssuerID{issuerA, issuerB},
		Quotas:     []Quota{{Issuer: issuerA, Activity: act1, Limit: 100}},
		Candidates: []Transaction{cycX, cycY, cycClose, rooted},
	}
	ledger, err := Prepare(p, acceptAll, acceptAllProof)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}

	cycleMask, _ := ledger.MaskFromIDs([]TxID{"cyc-x", "cyc-y", "cyc-close"})
	if ok, _ := ledger.Feasible(cycleMask); ok {
		t.Fatal("zero-cost self-funding cycle accepted")
	}
	// The closing back edge means no single member is independently funded:
	// cyc-x alone violates closure (its predecessor cyc-close is missing),
	// which is exactly the "selected set must contain all predecessors" rule.
	brokenClosureX, _ := ledger.MaskFromIDs([]TxID{"cyc-x"})
	if ok, _ := ledger.Feasible(brokenClosureX); ok {
		t.Fatal("cyc-x selected without its closing predecessor")
	}
	brokenClosure, _ := ledger.MaskFromIDs([]TxID{"cyc-y"})
	if ok, _ := ledger.Feasible(brokenClosure); ok {
		t.Fatal("transaction selected without its predecessor")
	}

	bestMask, bestValue := ledger.BruteForce()
	if bestValue != 2 {
		t.Fatalf("want 2 entitlements, got %d", bestValue)
	}
	if !equalIDs(ledger.SelectedIDs(bestMask), []TxID{"rooted-service"}) {
		t.Fatalf("unexpected winner %v", ledger.SelectedIDs(bestMask))
	}

	sol, err := Solve(p, acceptAll, acceptAllProof)
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	if err := VerifySolution(p, sol, acceptAll, acceptAllProof); err != nil {
		t.Fatalf("certificate verify: %v", err)
	}
	if sol.Entitlements != bestValue {
		t.Fatalf("solver value %d != brute %d", sol.Entitlements, bestValue)
	}
}
