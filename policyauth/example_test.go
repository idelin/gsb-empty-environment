package policyauth_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"fmt"

	"example.org/netsec/q0193/policyauth"
)

// Example shows the boot service authorizing a sealed key before unseal,
// and the monotonic counter rejecting a replayed token.
func Example() {
	ctx := context.Background()

	// The authority key pinned by the object's sealed policy.
	authorityKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	keyName, err := policyauth.PublicKeyName(&authorityKey.PublicKey)
	if err != nil {
		panic(err)
	}

	ref := policyauth.PolicyRef{DeviceID: "device-001", ObjectID: "disk-key"}
	currentPCRPolicy := policyauth.Digest{0x10}
	verifier, err := policyauth.NewVerifier(
		policyauth.ObjectPolicy{
			AuthorizedKeyName: keyName,
			ApprovedPolicy:    currentPCRPolicy,
			PolicyRef:         ref,
		},
		policyauth.NewMemoryCounter(0),
	)
	if err != nil {
		panic(err)
	}

	token, err := policyauth.SignToken(authorityKey, currentPCRPolicy, ref, 0)
	if err != nil {
		panic(err)
	}

	unsealed := false
	err = verifier.Authorize(ctx, token, &authorityKey.PublicKey, func(context.Context) error {
		unsealed = true
		return nil
	})
	fmt.Printf("first authorize: err=%v unsealed=%v\n", err, unsealed)

	// Replaying the same token after a successful release is a stale
	// generation and the sealed key is not released again.
	replayErr := verifier.Authorize(ctx, token, &authorityKey.PublicKey,
		func(context.Context) error { return nil })
	fmt.Printf("replay rejected: %v\n", errors.Is(replayErr, policyauth.ErrStaleGeneration))

	// Output:
	// first authorize: err=<nil> unsealed=true
	// replay rejected: true
}
