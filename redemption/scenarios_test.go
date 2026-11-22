package redemption

import "testing"

func equalIDs(got, want []TxID) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestChangeSharing: one change note feeds two competing downstream
// transactions; an accepted set may spend it only once.
func TestChangeSharing(t *testing.T) {
	p := Problem{
		Issuers: []IssuerID{issuerA, issuerB},
		Quotas:  []Quota{{Issuer: issuerA, Activity: act1, Limit: 100}},
		Candidates: []Transaction{
			{
				ID:      "root",
				Inputs:  []Input{originInput("root", 0, issuerA, act1, 10, "nf-root")},
				Outputs: []ChangeOutput{changeOut(issuerA, act1, 6, "root")},
				Spends:  []ServiceEntitlement{service("svc", issuerA, act1, 4, 4)},
			},
			{
				ID:     "child-1",
				Inputs: []Input{changeInput("root", 0, issuerA, act1, 6, "nf-c1", "change/root")},
				Spends: []ServiceEntitlement{service("svc", issuerA, act1, 3, 6)},
			},
			{
				ID:     "child-2",
				Inputs: []Input{changeInput("root", 0, issuerA, act1, 6, "nf-c2", "change/root")},
				Spends: []ServiceEntitlement{service("svc", issuerA, act1, 5, 6)},
			},
		},
	}
	ledger, err := Prepare(p, acceptAll, acceptAllProof)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	bestMask, bestValue := ledger.BruteForce()
	if bestValue != 9 {
		t.Fatalf("want 9 entitlements, got %d", bestValue)
	}
	if !equalIDs(ledger.SelectedIDs(bestMask), []TxID{"child-2", "root"}) {
		t.Fatalf("unexpected winner %v", ledger.SelectedIDs(bestMask))
	}
	badMask, _ := ledger.MaskFromIDs([]TxID{"root", "child-1", "child-2"})
	if ok, _ := ledger.Feasible(badMask); ok {
		t.Fatal("shared change spent twice was accepted")
	}

	sol, err := Solve(p, acceptAll, acceptAllProof)
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	if sol.Entitlements != bestValue ||
		!equalIDs(sol.Selected, []TxID{"child-2", "root"}) {
		t.Fatalf("solver mismatch: %+v", sol)
	}
	if err := VerifySolution(p, sol, acceptAll, acceptAllProof); err != nil {
		t.Fatalf("certificate verify: %v", err)
	}
}

// TestSameNullifierDifferentNotes: two branches expose the same nullifier on
// notes of different denominations. Each is valid alone but the pair is
// mutually exclusive.
func TestSameNullifierDifferentNotes(t *testing.T) {
	p := Problem{
		Issuers: []IssuerID{issuerA, issuerB},
		Quotas: []Quota{
			{Issuer: issuerA, Activity: act1, Limit: 100},
			{Issuer: issuerB, Activity: act1, Limit: 100},
		},
		Candidates: []Transaction{
			{
				ID:     "branch-small",
				Inputs: []Input{originInput("branch-small", 0, issuerA, act1, 5, "shared-nf")},
				Spends: []ServiceEntitlement{service("svc", issuerA, act1, 2, 5)},
			},
			{
				ID:     "branch-large",
				Inputs: []Input{originInput("branch-large", 0, issuerB, act1, 12, "shared-nf")},
				Spends: []ServiceEntitlement{service("svc", issuerB, act1, 7, 12)},
			},
			{
				ID:     "independent",
				Inputs: []Input{originInput("independent", 0, issuerA, act1, 3, "other-nf")},
				Spends: []ServiceEntitlement{service("svc", issuerA, act1, 1, 3)},
			},
		},
	}
	ledger, err := Prepare(p, acceptAll, acceptAllProof)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	bestMask, bestValue := ledger.BruteForce()
	if bestValue != 8 { // independent(1) + branch-large(7)
		t.Fatalf("want 8, got %d", bestValue)
	}
	if !equalIDs(ledger.SelectedIDs(bestMask), []TxID{"branch-large", "independent"}) {
		t.Fatalf("unexpected winner %v", ledger.SelectedIDs(bestMask))
	}
	badMask, _ := ledger.MaskFromIDs([]TxID{"branch-small", "branch-large"})
	if ok, _ := ledger.Feasible(badMask); ok {
		t.Fatal("same nullifier on different notes was accepted together")
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

// TestConfirmedPrefixConflict: a candidate reusing a nullifier already spent
// by the confirmed prefix is permanently blocked, while a candidate building
// on confirmed change remains selectable.
func TestConfirmedPrefixConflict(t *testing.T) {
	confirmed := Transaction{
		ID:      "confirmed-root",
		Inputs:  []Input{originInput("confirmed-root", 0, issuerA, act1, 10, "nf-confirmed")},
		Outputs: []ChangeOutput{changeOut(issuerA, act1, 7, "confirmed-root")},
		Spends:  []ServiceEntitlement{service("svc", issuerA, act1, 3, 3)},
	}
	p := Problem{
		Issuers:   []IssuerID{issuerA, issuerB},
		Quotas:    []Quota{{Issuer: issuerA, Activity: act1, Limit: 100}},
		Confirmed: []Transaction{confirmed},
		Candidates: []Transaction{
			{
				ID:     "replay",
				Inputs: []Input{originInput("replay", 0, issuerA, act1, 10, "nf-confirmed")},
				Spends: []ServiceEntitlement{service("svc", issuerA, act1, 9, 10)},
			},
			{
				ID:     "legit-child",
				Inputs: []Input{changeInput("confirmed-root", 0, issuerA, act1, 7, "nf-child", "change/confirmed-root")},
				Spends: []ServiceEntitlement{service("svc", issuerA, act1, 4, 7)},
			},
		},
	}
	ledger, err := Prepare(p, acceptAll, acceptAllProof)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	replayMask, _ := ledger.MaskFromIDs([]TxID{"replay"})
	if ok, _ := ledger.Feasible(replayMask); ok {
		t.Fatal("prefix-conflicting candidate accepted")
	}
	bestMask, bestValue := ledger.BruteForce()
	if bestValue != 4 {
		t.Fatalf("want 4, got %d", bestValue)
	}
	if !equalIDs(ledger.SelectedIDs(bestMask), []TxID{"legit-child"}) {
		t.Fatalf("unexpected winner %v", ledger.SelectedIDs(bestMask))
	}
	sol, err := Solve(p, acceptAll, acceptAllProof)
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	if err := VerifySolution(p, sol, acceptAll, acceptAllProof); err != nil {
		t.Fatalf("certificate verify: %v", err)
	}
}
