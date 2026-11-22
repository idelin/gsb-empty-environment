package disclosure

import (
	"crypto/ed25519"
	"fmt"
)

// TombstoneAuthorizationMessage is the message an external erasure authority
// signs to authorize a tombstone for the target leaf digest.
func TombstoneAuthorizationMessage(target Hash) []byte {
	return append([]byte("audit-tombstone/v1\x00"), target[:]...)
}

// SignTombstoneAuthorization signs an erasure authorization with Ed25519.
func SignTombstoneAuthorization(privateKey ed25519.PrivateKey, target Hash) ([]byte, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("disclosure: invalid Ed25519 private key")
	}
	return ed25519.Sign(privateKey, TombstoneAuthorizationMessage(target)), nil
}

// Ed25519TombstoneAuthorizer verifies an external erasure authority signature
// over the tombstone's target digest.
func Ed25519TombstoneAuthorizer(publicKey ed25519.PublicKey) func(Hash, []byte) error {
	return func(target Hash, authorization []byte) error {
		if len(publicKey) != ed25519.PublicKeySize ||
			!ed25519.Verify(publicKey, TombstoneAuthorizationMessage(target), authorization) {
			return ErrUnauthorizedTombstone
		}
		return nil
	}
}
