package netsec_test

import (
	"context"
	"fmt"
	"log"

	"example.org/netsec/q0160"
)

// A client selects one issuer for its session. The verifier will only accept
// tokens whose "iss" claim equals that issuer, and it will only check the
// signature against that issuer's JWKS. A token whose "iss" was rewritten to a
// different party - even one signed with a key cached locally - is rejected.
func ExampleVerifier() {
	verifier, err := netsec.NewVerifier(netsec.Config{
		Issuer:   "https://login.example.org",
		Audience: []string{"my-client-id"},
	})
	if err != nil {
		log.Fatal(err)
	}

	idToken, err := verifier.Verify(context.Background(), "header.payload.signature")
	if err != nil {
		// Expected for the placeholder token; real callers handle the typed
		// errors such as netsec.ErrIssuerMismatch or netsec.ErrSignature.
		fmt.Println("rejected")
		return
	}
	fmt.Println("accepted token for", idToken.Issuer)
	// Output: rejected
}
