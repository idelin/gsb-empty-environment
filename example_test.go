package netsec

import (
	"fmt"
)

func ExampleDictionary() {
	dict := New()

	secretOrigin := Origin{
		Tenant: "tenant-a",
		Input:  SumDigest([]byte("tenant-a/private/input.bin")),
		Public: false,
	}
	publicOrigin := Origin{
		Tenant: "tenant-b",
		Input:  SumDigest([]byte("published/protocol/spec")),
		Public: true,
	}

	// Same bytes, different sources: restrictions are never merged by content.
	secretID, _ := dict.Add([]byte("TOKEN"), secretOrigin)
	publicID, _ := dict.Add([]byte("TOKEN"), publicOrigin)

	// Derivation unions provenance; the public parent cannot cleanse the secret.
	derivedID, _ := dict.Derive([]byte("TOKEN-suffix"), secretID, publicID)

	tenantB, _ := dict.Snapshot([]Tenant{"tenant-b"})
	fmt.Println("tenant-b visible:", tenantB.Len())

	both, _ := dict.Snapshot([]Tenant{"tenant-a", "tenant-b"})
	fmt.Println("intersection visible:", both.Len())

	// Revoking the source invalidates the secret entry and every derivative.
	_ = dict.Revoke("tenant-a", secretOrigin.Input)
	dead, _ := dict.Entry(derivedID)
	fmt.Println("derived entry revoked:", dead.Revoked)

	// Output:
	// tenant-b visible: 1
	// intersection visible: 3
	// derived entry revoked: true
}
