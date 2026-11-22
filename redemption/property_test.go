package redemption

import (
	"encoding/json"
	"math/rand"
	"testing"
)

// TestRandomAgainstBruteForce fuzzes many small instances and requires the
// branch-and-bound solver, its certificate and the full-enumeration oracle to
// agree on both value and the tie-broken transaction-ID sequence.
func TestRandomAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(20261122))
	for iter := 0; iter < 200; iter++ {
		n := 1 + rng.Intn(12)
		p := randomProblem(rng, n)
		ledger, err := Prepare(p, acceptAll, acceptAllProof)
		if err != nil {
			t.Fatalf("iter %d prepare: %v", iter, err)
		}
		bruteMask, bruteValue := ledger.BruteForce()
		sol, err := Solve(p, acceptAll, acceptAllProof)
		if err != nil {
			t.Fatalf("iter %d solve: %v", iter, err)
		}
		if err := VerifySolution(p, sol, acceptAll, acceptAllProof); err != nil {
			t.Fatalf("iter %d certificate: %v", iter, err)
		}
		if sol.Entitlements != bruteValue {
			t.Fatalf("iter %d value solver=%d brute=%d", iter, sol.Entitlements, bruteValue)
		}
		if !equalIDs(sol.Selected, ledger.SelectedIDs(bruteMask)) {
			t.Fatalf("iter %d ids solver=%v brute=%v", iter, sol.Selected, ledger.SelectedIDs(bruteMask))
		}
	}
}

// TestTieBreak uses equal-value independent transactions and checks the
// ascending transaction-ID lexicographic rule (one-wins at first difference).
func TestTieBreak(t *testing.T) {
	p := Problem{
		Issuers: []IssuerID{issuerA, issuerB},
		Quotas:  []Quota{{Issuer: issuerA, Activity: act1, Limit: 100}},
		Candidates: []Transaction{
			{
				ID:     "zeta",
				Inputs: []Input{originInput("zeta", 0, issuerA, act1, 2, "nf-z")},
				Spends: []ServiceEntitlement{service("svc", issuerA, act1, 1, 2)},
			},
			{
				ID:     "alpha",
				Inputs: []Input{originInput("alpha", 0, issuerA, act1, 2, "nf-a")},
				Spends: []ServiceEntitlement{service("svc", issuerA, act1, 1, 2)},
			},
		},
	}
	sol, err := Solve(p, acceptAll, acceptAllProof)
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	// Both selected (distinct nullifiers), but if a quota forced a single
	// choice alpha must win. First assert the normal case.
	if !equalIDs(sol.Selected, []TxID{"alpha", "zeta"}) {
		t.Fatalf("unexpected selection %v", sol.Selected)
	}

	// Tight quota: only one fits; alpha has the smaller ID and wins.
	p.Quotas[0].Limit = 2
	sol, err = Solve(p, acceptAll, acceptAllProof)
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	if !equalIDs(sol.Selected, []TxID{"alpha"}) {
		t.Fatalf("tie-break want [alpha], got %v", sol.Selected)
	}
	if err := VerifySolution(p, sol, acceptAll, acceptAllProof); err != nil {
		t.Fatalf("certificate: %v", err)
	}
}

// TestJSONRoundTrip ensures the import/export package preserves semantics.
func TestJSONRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	p := randomProblem(rng, 8)
	raw, err := EncodeProblem(p)
	if err != nil {
		t.Fatalf("encode problem: %v", err)
	}
	decoded, err := DecodeProblem(raw)
	if err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	if CanonicalDigest(p) != CanonicalDigest(decoded) {
		t.Fatal("digest changed across JSON round trip")
	}
	sol, err := Solve(p, acceptAll, acceptAllProof)
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	solRaw, err := EncodeSolution(sol)
	if err != nil {
		t.Fatalf("encode solution: %v", err)
	}
	sol2, err := DecodeSolution(solRaw)
	if err != nil {
		t.Fatalf("decode solution: %v", err)
	}
	if err := VerifySolution(decoded, sol2, acceptAll, acceptAllProof); err != nil {
		t.Fatalf("verify after round trip: %v", err)
	}
	// Generic JSON sanity check.
	var generic map[string]any
	if err := json.Unmarshal(solRaw, &generic); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
}

// TestMax22Candidates exercises the full 22-candidate envelope, requiring the
// certificate to verify and match brute force within the test budget.
func TestMax22Candidates(t *testing.T) {
	rng := rand.New(rand.NewSource(22))
	p := randomProblem(rng, 22)
	ledger, err := Prepare(p, acceptAll, acceptAllProof)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if ledger.N() != 22 {
		t.Fatalf("want 22 candidates, got %d", ledger.N())
	}
	sol, err := Solve(p, acceptAll, acceptAllProof)
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	if err := VerifySolution(p, sol, acceptAll, acceptAllProof); err != nil {
		t.Fatalf("certificate: %v", err)
	}
	bruteMask, bruteValue := ledger.BruteForce()
	if sol.Entitlements != bruteValue ||
		!equalIDs(sol.Selected, ledger.SelectedIDs(bruteMask)) {
		t.Fatalf("22-candidate mismatch solver=%v brute=%v", sol.Selected, ledger.SelectedIDs(bruteMask))
	}
	if len(sol.Certificate.Nodes) > 1<<22 {
		t.Fatalf("certificate too large: %d nodes", len(sol.Certificate.Nodes))
	}
	t.Logf("22-candidate certificate nodes: %d", len(sol.Certificate.Nodes))
}

// TestConservationRejected ensures cross-key value movement and broken
// conservation are rejected at prepare time.
func TestConservationRejected(t *testing.T) {
	p := Problem{
		Issuers: []IssuerID{issuerA, issuerB},
		Quotas:  []Quota{{Issuer: issuerA, Activity: act1, Limit: 100}},
		Candidates: []Transaction{
			{
				ID: "leak",
				Inputs: []Input{
					originInput("leak", 0, issuerA, act1, 5, "nf-1"),
				},
				Outputs: []ChangeOutput{changeOut(issuerA, act1, 2, "leak")},
				Spends:  []ServiceEntitlement{service("svc", issuerA, act1, 1, 2)},
			},
		},
	}
	if _, err := Prepare(p, acceptAll, acceptAllProof); err == nil {
		t.Fatal("conservation violation accepted")
	}
}

// TestHostVerifiersInvoked checks that injected signature and proof verifiers
// are actually consulted and their failures surface.
func TestHostVerifiersInvoked(t *testing.T) {
	p := Problem{
		Issuers: []IssuerID{issuerA, issuerB},
		Quotas:  []Quota{{Issuer: issuerA, Activity: act1, Limit: 100}},
		Candidates: []Transaction{
			{
				ID:     "tx",
				Inputs: []Input{originInput("tx", 0, issuerA, act1, 3, "nf")},
				Spends: []ServiceEntitlement{service("svc", issuerA, act1, 1, 3)},
			},
		},
	}
	badSig := func(_ OriginNote) error { return ErrSignature }
	if _, err := Prepare(p, badSig, acceptAllProof); err == nil {
		t.Fatal("bad signature verifier ignored")
	}
	badProof := func(_ Transaction) error { return ErrProof }
	if _, err := Prepare(p, acceptAll, badProof); err == nil {
		t.Fatal("bad proof verifier ignored")
	}
}
