package netsec

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
)

// ErrUnknownIdentity is returned when no registered key matches an identity.
var ErrUnknownIdentity = errors.New("netsec: no signing key registered for identity")

// ErrDuplicateIdentity is returned when an identity is registered twice.
var ErrDuplicateIdentity = errors.New("netsec: signing key already registered for identity")

// Signer abstracts the identity host's key holder so the delegation engine
// stays independent of concrete key management.
type Signer interface {
	// Identity is the principal whose key signs tokens.
	Identity() Identity
	// Sign signs the canonical signing bytes of a token.
	Sign(message []byte) ([]byte, error)
}

// Verifier checks signatures against registered public keys.
type Verifier interface {
	Verify(identity Identity, message, signature []byte) bool
}

// SyntheticSigner is an in-memory Ed25519 signer used by identity hosts and
// tests. It keeps the private key inside the process; no key material is
// written to disk.
type SyntheticSigner struct {
	identity   Identity
	privateKey ed25519.PrivateKey
}

// NewSyntheticSigner generates a fresh Ed25519 key pair for identity.
func NewSyntheticSigner(identity Identity) (*SyntheticSigner, ed25519.PublicKey, error) {
	if err := identity.Validate(); err != nil {
		return nil, nil, err
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	return &SyntheticSigner{identity: identity, privateKey: privateKey}, publicKey, nil
}

func (s *SyntheticSigner) Identity() Identity { return s.identity }

func (s *SyntheticSigner) Sign(message []byte) ([]byte, error) {
	return ed25519.Sign(s.privateKey, message), nil
}

// KeyRegistry maps principals to their public keys. It is safe for
// concurrent use; registration happens up front while verification happens on
// the hot path.
type KeyRegistry struct {
	keys map[Identity]ed25519.PublicKey
}

// NewKeyRegistry creates an empty registry.
func NewKeyRegistry() *KeyRegistry {
	return &KeyRegistry{keys: make(map[Identity]ed25519.PublicKey)}
}

// Register records the public key for signer's identity.
func (r *KeyRegistry) Register(signer *SyntheticSigner, publicKey ed25519.PublicKey) error {
	id := signer.Identity()
	if _, exists := r.keys[id]; exists {
		return ErrDuplicateIdentity
	}
	r.keys[id] = publicKey
	return nil
}

// Has reports whether identity has a registered verification key.
func (r *KeyRegistry) Has(identity Identity) bool {
	_, ok := r.keys[identity]
	return ok
}

// Verify reports whether signature is valid for message under identity's key.
// Unknown identities fail closed.
func (r *KeyRegistry) Verify(identity Identity, message, signature []byte) bool {
	publicKey, ok := r.keys[identity]
	if !ok {
		return false
	}
	return ed25519.Verify(publicKey, message, signature)
}
