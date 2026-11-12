package netsec

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
)

// Signer signs a canonical window-root message. Implementations must be
// deterministic per key; the host may supply an HSM-backed adapter behind
// this interface.
type Signer interface {
	// KeyID identifies the signing key so verifiers can select the right
	// public key.
	KeyID() string
	// Sign returns a signature over message.
	Sign(message []byte) ([]byte, error)
}

// Verifier checks a window-root signature.
type Verifier interface {
	KeyID() string
	Verify(message, signature []byte) bool
}

// Ed25519Key is a Signer and Verifier backed by a standard Ed25519 key
// pair. It suits hosts that keep the sealing key in process; hosts with an
// external KMS implement Signer directly.
type Ed25519Key struct {
	id     string
	secret ed25519.PrivateKey
	public ed25519.PublicKey
}

// GenerateEd25519Key creates a fresh Ed25519 sealing key with the given key
// identifier. It is intended for tests and for hosts that manage their own
// on-disk key material.
func GenerateEd25519Key(keyID string) (*Ed25519Key, error) {
	if keyID == "" {
		return nil, fmt.Errorf("barrier: key id must not be empty")
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &Ed25519Key{id: keyID, secret: priv, public: pub}, nil
}

// NewEd25519Verifier wraps an Ed25519 public key for signature checks.
func NewEd25519Verifier(keyID string, public ed25519.PublicKey) (Verifier, error) {
	if len(public) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("barrier: invalid Ed25519 public key size %d", len(public))
	}
	return &Ed25519Key{id: keyID, public: public}, nil
}

// KeyID returns the key identifier.
func (k *Ed25519Key) KeyID() string { return k.id }

// Sign signs message with the Ed25519 private key.
func (k *Ed25519Key) Sign(message []byte) ([]byte, error) {
	if len(k.secret) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("barrier: %q is a verifier-only key", k.id)
	}
	return ed25519.Sign(k.secret, message), nil
}

// Verify checks an Ed25519 signature over message.
func (k *Ed25519Key) Verify(message, signature []byte) bool {
	return ed25519.Verify(k.public, message, signature)
}

// PublicKey exposes the raw public key for distribution to verifiers.
func (k *Ed25519Key) PublicKey() ed25519.PublicKey {
	return append(ed25519.PublicKey(nil), k.public...)
}
