package targetverify

import (
	"crypto/ed25519"
	"fmt"
)

// Key verifies signatures over the raw signed metadata bytes.
type Key interface {
	ID() string
	Verify(message, signature []byte) bool
}

// Ed25519Key is a Key backed by an Ed25519 public key.
type Ed25519Key struct {
	id     string
	public ed25519.PublicKey
}

// NewEd25519Key builds a named Ed25519 verification key.
func NewEd25519Key(id string, public ed25519.PublicKey) (*Ed25519Key, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: ed25519 key has empty id", ErrInvalidConfig)
	}
	if len(public) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%w: ed25519 key %q must be %d bytes",
			ErrInvalidConfig, id, ed25519.PublicKeySize)
	}
	copied := make(ed25519.PublicKey, len(public))
	copy(copied, public)
	return &Ed25519Key{id: id, public: copied}, nil
}

// ID returns the key identifier used in metadata signatures.
func (k *Ed25519Key) ID() string { return k.id }

// Verify reports whether signature is valid for message under this key.
func (k *Ed25519Key) Verify(message, signature []byte) bool {
	return ed25519.Verify(k.public, message, signature)
}

// Keyring holds the trusted public keys for one repository role.
type Keyring struct {
	keys map[string]Key
}

// NewKeyring builds a keyring, rejecting nil or duplicate keys.
func NewKeyring(keys ...Key) (*Keyring, error) {
	keyring := &Keyring{keys: make(map[string]Key, len(keys))}
	for _, key := range keys {
		if key == nil {
			return nil, fmt.Errorf("%w: nil key in keyring", ErrInvalidConfig)
		}
		if key.ID() == "" {
			return nil, fmt.Errorf("%w: key with empty id", ErrInvalidConfig)
		}
		if _, exists := keyring.keys[key.ID()]; exists {
			return nil, fmt.Errorf("%w: duplicate key id %q", ErrInvalidConfig, key.ID())
		}
		keyring.keys[key.ID()] = key
	}
	return keyring, nil
}

// Len returns the number of trusted keys.
func (kr *Keyring) Len() int { return len(kr.keys) }

// CountValidSignatures verifies the given signatures and returns the number
// of distinct trusted key IDs that produced a valid signature. Unknown key
// IDs, invalid signatures and repeated key IDs are ignored.
func (kr *Keyring) CountValidSignatures(message []byte, signatures []Signature) int {
	seen := make(map[string]struct{}, len(signatures))
	valid := 0
	for _, signature := range signatures {
		if _, duplicate := seen[signature.KeyID]; duplicate {
			continue
		}
		seen[signature.KeyID] = struct{}{}
		key, trusted := kr.keys[signature.KeyID]
		if !trusted {
			continue
		}
		if key.Verify(message, signature.Sig) {
			valid++
		}
	}
	return valid
}
