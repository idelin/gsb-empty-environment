package netsec_test

import (
	"crypto/ed25519"
	"fmt"

	"example.org/netsec/q0480"
)

// ExampleVerifier shows how a verification host checks a negotiation log and
// distinguishes an authenticated transcript from a secure outcome.
func ExampleVerifier() {
	pubA, privA, _ := ed25519.GenerateKey(nil)
	pubB, privB, _ := ed25519.GenerateKey(nil)
	sessionID := []byte("session-42")

	recorder, err := netsec.NewRecorder(sessionID, privA, privB)
	if err != nil {
		panic(err)
	}
	_ = recorder.OfferA([]string{"AES-GCM-256", "RC4"})
	_ = recorder.OfferB([]string{"AES-GCM-256", "RC4"})
	_ = recorder.Select("AES-GCM-256")
	_ = recorder.Confirm()
	transcript := recorder.Transcript()

	verifier, err := netsec.New(netsec.Policy{
		SessionID:       sessionID,
		PartyAKey:       pubA,
		PartyBKey:       pubB,
		PreferenceOrder: []string{"AES-GCM-256", "RC4"},
		Acceptable:      map[string]bool{"AES-GCM-256": true},
	})
	if err != nil {
		panic(err)
	}

	report := verifier.Verify(transcript)
	fmt.Println("authentic:", report.Authentic)
	fmt.Println("secure:", report.Secure)
	fmt.Println("selected:", report.Selected)
	fmt.Println("intersection:", report.Intersection)
	fmt.Println("clues:", len(report.Clues))
	// Output:
	// authentic: true
	// secure: true
	// selected: AES-GCM-256
	// intersection: [AES-GCM-256 RC4]
	// clues: 0
}
