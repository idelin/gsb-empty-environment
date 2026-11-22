package redemption_test

import (
	"fmt"

	"prescreen.local/ddd-9491-20261122-v2/redemption"
)

// Example shows the end-to-end host flow: build a problem, inject the
// signature and proof verifiers the host trusts, solve, and independently
// verify the optimality certificate.
func Example() {
	const (
		issuer redemption.IssuerID   = "bank"
		act    redemption.ActivityID = "gen-7"
	)
	note := redemption.OriginNote{
		Issuer: issuer, Activity: act, Commitment: "cm-1",
		Denomination: 10, Nullifier: "nf-1",
	}
	problem := redemption.Problem{
		Issuers: []redemption.IssuerID{issuer},
		Quotas:  []redemption.Quota{{Issuer: issuer, Activity: act, Limit: 100}},
		Candidates: []redemption.Transaction{
			{
				ID: "redeem-1",
				Inputs: []redemption.Input{{
					Source:     redemption.NoteSource{Origin: &note},
					Issuer:     issuer,
					Activity:   act,
					Commitment: "cm-1",
					Amount:     10,
					Nullifier:  "nf-1",
				}},
				Outputs: []redemption.ChangeOutput{
					{Issuer: issuer, Activity: act, Commitment: "cm-chg", Amount: 4},
				},
				Spends: []redemption.ServiceEntitlement{
					{Service: "vpn", Issuer: issuer, Activity: act, Units: 6, Cost: 6},
				},
			},
		},
	}

	// Host-injected cryptographic checks. Passing nil disables them; a real
	// host would verify issuer signatures and zero-knowledge redemption
	// proofs here.
	verifySig := func(redemption.OriginNote) error { return nil }
	verifyProof := func(redemption.Transaction) error { return nil }

	solution, err := redemption.Solve(problem, verifySig, verifyProof)
	if err != nil {
		panic(err)
	}
	if err := redemption.VerifySolution(problem, solution, verifySig, verifyProof); err != nil {
		panic(err)
	}
	fmt.Println(solution.Selected, solution.Entitlements)
	// Output: [redeem-1] 6
}
