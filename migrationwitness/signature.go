package migrationwitness

import (
	"crypto/ed25519"
	"errors"
)

// ErrUntrustedSigner is returned when a statement is signed by an unknown key.
var ErrUntrustedSigner = errors.New("migrationwitness: untrusted signer")

// ErrBadSignature is returned when a signature does not verify.
var ErrBadSignature = errors.New("migrationwitness: bad signature")

// TrustStore holds the public keys authorized to sign migration evidence.
type TrustStore struct {
	keys map[KeyID]ed25519.PublicKey
}

// NewTrustStore creates an empty trust store.
func NewTrustStore() *TrustStore {
	return &TrustStore{keys: make(map[KeyID]ed25519.PublicKey)}
}

// Add registers a trusted signer key.
func (t *TrustStore) Add(id KeyID, public ed25519.PublicKey) {
	cp := append(ed25519.PublicKey(nil), public...)
	t.keys[id] = cp
}

func (t *TrustStore) verify(signer KeyID, sig []byte, body any) error {
	pub, ok := t.keys[signer]
	if !ok {
		return ErrUntrustedSigner
	}
	if !ed25519.Verify(pub, canonicalize(body), sig) {
		return ErrBadSignature
	}
	return nil
}

// Signed is a canonicalized body together with its signer and signature.
type Signed[T any] struct {
	Body      T
	Signer    KeyID
	Signature []byte
}

// Sign canonicalizes and signs a body with the given private key.
func Sign[T any](body T, signer KeyID, priv ed25519.PrivateKey) Signed[T] {
	sig := ed25519.Sign(priv, canonicalize(body))
	return Signed[T]{Body: body, Signer: signer, Signature: sig}
}

// Verify checks the envelope against the trust store.
func (e Signed[T]) Verify(store *TrustStore) error {
	return store.verify(e.Signer, e.Signature, e.Body)
}
