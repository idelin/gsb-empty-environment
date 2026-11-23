package authsolve

import (
	"crypto/ed25519"
	"fmt"
)

// SignCredential signs the credential's canonical signed message.  It is a
// convenience for release-host import code; verification uses
// Credential.VerifySignature.
func SignCredential(priv ed25519.PrivateKey, c *Credential) error {
	if c == nil {
		return fmt.Errorf("%w: nil credential", ErrInvalidInput)
	}
	if len(priv) != ed25519.PrivateKeySize {
		return fmt.Errorf("%w: invalid Ed25519 private key", ErrSignature)
	}
	c.Signature = ed25519.Sign(priv, c.SigningMessage())
	return nil
}

// SignCredentialMessage signs an explicit message and records it on the
// credential so verification is unambiguous.
func SignCredentialMessage(priv ed25519.PrivateKey, c *Credential, message []byte) error {
	if c == nil {
		return fmt.Errorf("%w: nil credential", ErrInvalidInput)
	}
	if len(priv) != ed25519.PrivateKeySize {
		return fmt.Errorf("%w: invalid Ed25519 private key", ErrSignature)
	}
	c.Message = append([]byte(nil), message...)
	c.Signature = ed25519.Sign(priv, message)
	return nil
}
