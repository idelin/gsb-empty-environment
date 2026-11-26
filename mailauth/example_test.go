package mailauth_test

import (
	"fmt"

	"formal.local/w27-b95a8bd059/mailauth"
)

// ExampleVerifier shows the offline verification entry point used by an
// embedded mail security host. Keys come from a host supplied local store; the
// package itself performs no network access.
func ExampleVerifier() {
	store := mailauth.NewMemoryKeyStore()
	// Hosts populate the store from their own local, pre-fetched key material.
	// store.Register("example.com", "sel", rsaPublicKey)

	verifier := mailauth.NewVerifier(store, mailauth.DefaultPolicy())

	raw := []byte("From: alice@example.com\r\n" +
		"Subject: hello\r\n" +
		"\r\n" +
		"body\r\n")

	verdict, err := verifier.VerifyMessage(raw)
	if err != nil {
		fmt.Println("parse error:", err)
		return
	}
	if !verdict.Accepted {
		fmt.Println("rejected:", verdict.Reason)
		for _, f := range verdict.Findings {
			fmt.Printf(" - %s (%s): %s\n", f.Code, f.Severity, f.Message)
		}
		return
	}
	fmt.Println("accepted")
	// Output:
	// rejected: no valid signature covers the From header field
	//  - no_dkim_signature (high): message carries no DKIM-Signature header field
}

// Finding codes can be matched directly to drive gateway actions.
func ExampleMessageVerdict_HasCode() {
	var verdict *mailauth.MessageVerdict
	if verdict != nil && verdict.HasCode(mailauth.FindingBodyLengthInjection) {
		fmt.Println("quarantine: truncation injection")
	}
}
