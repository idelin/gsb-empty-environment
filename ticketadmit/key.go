// Package ticketadmit implements admission control for TLS session tickets
// used by PSK resumption handlers.
//
// Tickets are sealed with AES-256-GCM using a key ring holding at most two
// ticket key generations (the current and the previous one). The plaintext
// claims bind the ticket to the virtual host context in which it was issued:
// SNI, ALPN, the SHA-256 of the client certificate (empty when no client
// certificate was presented), cipher suite and issuance time. On resumption
// the Admitter enforces this binding so that a ticket obtained on a lenient
// virtual host cannot be replayed onto a virtual host that requires client
// authentication.
package ticketadmit

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"errors"
)

// KeySize is the required size of a ticket key, in bytes. AES-256-GCM is
// used for authenticated encryption.
const KeySize = 32

// nonceSize is the AES-GCM standard nonce length.
const nonceSize = 12

// keyInfo is the HKDF info string used to separate ticket AEAD keys from any
// other use of the raw ticket key.
const keyInfo = "ticketadmit/v1 aead-key"

// ErrInvalidKey is returned when a ticket key has the wrong size or when a
// key ring is constructed without a current key.
var ErrInvalidKey = errors.New("ticketadmit: invalid ticket key")

// Key is a ticket encryption key generation.
type Key struct {
	// Generation identifies the key generation. It is authenticated as part
	// of the ticket envelope and must be unique for every rotation.
	Generation uint32

	// Secret is the raw key material; it must be KeySize random bytes.
	Secret []byte

	aead cipher.AEAD
}

// NewKey validates the secret and derives the AEAD bound to the generation.
func NewKey(generation uint32, secret []byte) (*Key, error) {
	if len(secret) != KeySize {
		return nil, ErrInvalidKey
	}
	// Copy so later mutation of the caller's slice cannot change the ring.
	raw := make([]byte, KeySize)
	copy(raw, secret)

	// HKDF-Expand-Label style derivation keeps the raw ticket key from being
	// used directly as the AES key and binds the derivation to this package.
	aeadKey, err := hkdf.Key(sha256.New, raw, nil, keyInfo, KeySize)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(aeadKey)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Key{Generation: generation, Secret: raw, aead: aead}, nil
}

// GenerateKey creates a fresh random key for the given generation.
func GenerateKey(generation uint32) (*Key, error) {
	secret := make([]byte, KeySize)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	return NewKey(generation, secret)
}

// KeyRing holds the current key generation and, optionally, the previous one.
// Sealing always uses the current key; opening tries the current key first and
// then the previous key, supporting zero-downtime rotation.
type KeyRing struct {
	current  *Key
	previous *Key
}

// NewKeyRing builds a ring. previous may be nil while only one generation is
// deployed. Both keys must be valid, and the previous generation must differ
// from the current one.
func NewKeyRing(current, previous *Key) (*KeyRing, error) {
	if current == nil || current.aead == nil {
		return nil, ErrInvalidKey
	}
	if previous != nil {
		if previous.aead == nil {
			return nil, ErrInvalidKey
		}
		if previous.Generation == current.Generation {
			return nil, ErrInvalidKey
		}
	}
	return &KeyRing{current: current, previous: previous}, nil
}

// Rotate demotes current to the previous generation and installs next as the
// current key. Tickets sealed with the old current key remain openable; the
// generation that was previously previous is retired and can no longer open
// tickets (a three-generations-old ticket therefore becomes unknown).
func (r *KeyRing) Rotate(next *Key) (*KeyRing, error) {
	if next == nil || next.aead == nil {
		return nil, ErrInvalidKey
	}
	if next.Generation == r.current.Generation {
		return nil, ErrInvalidKey
	}
	if r.previous != nil && next.Generation == r.previous.Generation {
		return nil, ErrInvalidKey
	}
	return &KeyRing{current: next, previous: r.current}, nil
}

// lookup returns the key for a generation, checking current first. It returns
// nil when neither deployed generation matches.
func (r *KeyRing) lookup(generation uint32) *Key {
	if r.current.Generation == generation {
		return r.current
	}
	if r.previous != nil && r.previous.Generation == generation {
		return r.previous
	}
	return nil
}
