package handoff

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
)

// SignedStatement is a controller-signed exact announcement/withdrawal batch.
type SignedStatement struct {
	Statement Statement `json:"statement"`
	Signature []byte    `json:"signature"`
}

// SignStatement signs the exact statement digest.
func SignStatement(key ed25519.PrivateKey, st Statement) SignedStatement {
	d := st.Digest()
	msg := append([]byte("handoff-statement-signature-v1\n"), d[:]...)
	return SignedStatement{Statement: st, Signature: ed25519.Sign(key, msg)}
}

// Verify checks the controller signature and exact statement digest.
func (ss SignedStatement) Verify(key ed25519.PublicKey) error {
	if len(key) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: statement key length %d", ErrBadSignature, len(key))
	}
	d := ss.Statement.Digest()
	msg := append([]byte("handoff-statement-signature-v1\n"), d[:]...)
	if len(ss.Signature) != ed25519.SignatureSize || !ed25519.Verify(key, msg, ss.Signature) {
		return ErrBadSignature
	}
	return nil
}

// GenerateControllerKey returns a signing key for signed statements.
func GenerateControllerKey() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	return ed25519.GenerateKey(rand.Reader)
}
