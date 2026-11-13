package netsec

import (
	"crypto/ed25519"
	"errors"
	"sync"
	"time"
)

// Sentinel errors returned by the trusted key registry.
var (
	ErrUnknownKey           = errors.New("netsec: no trusted key registered for (AS, keyGeneration)")
	ErrRevokedKey           = errors.New("netsec: signing key has been revoked")
	ErrExpiredKey           = errors.New("netsec: signing key is past its expiry time")
	ErrKeyNotYetValid       = errors.New("netsec: signing key is not valid yet")
	ErrKeyAlreadyRegistered = errors.New("netsec: key generation already registered for AS")
	ErrInvalidPublicKey     = errors.New("netsec: invalid Ed25519 public key")
)

type keyID struct {
	as         uint32
	generation uint64
}

// TrustedKey is one Ed25519 verification key registered for a specific AS and
// key generation. A record is only accepted when its (signerAS, keyGeneration)
// resolves to a TrustedKey that is neither revoked nor expired at verification
// time. Zero time fields mean "no lower/upper time bound".
type TrustedKey struct {
	AS         uint32
	Generation uint64
	Public     ed25519.PublicKey
	NotBefore  time.Time
	NotAfter   time.Time
	Revoked    bool
}

// KeyRegistry is the trusted table of public keys, indexed by AS and key
// generation. It is safe for concurrent use.
type KeyRegistry struct {
	mu   sync.RWMutex
	keys map[keyID]TrustedKey
}

// NewKeyRegistry creates an empty trusted key table.
func NewKeyRegistry() *KeyRegistry {
	return &KeyRegistry{keys: make(map[keyID]TrustedKey)}
}

// Register adds a trusted key generation. Re-registering the same
// (AS, generation) pair is rejected so that a rotation cannot silently
// overwrite an existing entry.
func (r *KeyRegistry) Register(k TrustedKey) error {
	if len(k.Public) != ed25519.PublicKeySize {
		return ErrInvalidPublicKey
	}
	id := keyID{as: k.AS, generation: k.Generation}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.keys[id]; exists {
		return ErrKeyAlreadyRegistered
	}
	r.keys[id] = k
	return nil
}

// Revoke marks a registered key generation as revoked. A revoked key can never
// validate a record again.
func (r *KeyRegistry) Revoke(as uint32, generation uint64) error {
	id := keyID{as: as, generation: generation}
	r.mu.Lock()
	defer r.mu.Unlock()
	k, ok := r.keys[id]
	if !ok {
		return ErrUnknownKey
	}
	k.Revoked = true
	r.keys[id] = k
	return nil
}

// Lookup resolves a verification key for (as, generation) and enforces the
// revocation and validity-window state at time now.
func (r *KeyRegistry) Lookup(as uint32, generation uint64, now time.Time) (TrustedKey, error) {
	r.mu.RLock()
	k, ok := r.keys[keyID{as: as, generation: generation}]
	r.mu.RUnlock()
	if !ok {
		return TrustedKey{}, ErrUnknownKey
	}
	if k.Revoked {
		return TrustedKey{}, ErrRevokedKey
	}
	if !k.NotBefore.IsZero() && now.Before(k.NotBefore) {
		return TrustedKey{}, ErrKeyNotYetValid
	}
	if !k.NotAfter.IsZero() && !now.Before(k.NotAfter) {
		return TrustedKey{}, ErrExpiredKey
	}
	return k, nil
}
