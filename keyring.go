package netsec

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"sync"
	"time"
)

// KeyInfo describes a handle-encryption key without exposing its material.
type KeyInfo struct {
	ID                  string
	Active              bool
	RetiredAt           time.Time
	GraceUntil          time.Time
	MaxObjectGeneration uint64
}

type keyEntry struct {
	material            [32]byte
	active              bool
	retiredAt           time.Time
	graceUntil          time.Time
	maxObjectGeneration uint64
}

// KeyRing holds the active AEAD key and a history of retired keys.  Retired
// keys remain usable only until GraceUntil, and only for object generations
// at or below the cut-off captured when they were retired.
type KeyRing struct {
	mu     sync.RWMutex
	active string
	grace  time.Duration
	keys   map[string]*keyEntry
}

// NewKeyRing creates a key ring with one active AES-256-GCM key.  secret must
// be exactly 32 bytes.  grace is the period during which the key remains
// available for verification after the next Rotate call.
func NewKeyRing(activeID string, secret []byte, grace time.Duration) (*KeyRing, error) {
	if err := validateKeyID(activeID); err != nil {
		return nil, err
	}
	if len(secret) != 32 {
		return nil, fmt.Errorf("netsec: AES-256 key must be 32 bytes, got %d", len(secret))
	}
	if grace < 0 {
		return nil, fmt.Errorf("netsec: negative key grace period")
	}
	var material [32]byte
	copy(material[:], secret)
	kr := &KeyRing{
		active: activeID,
		grace:  grace,
		keys: map[string]*keyEntry{
			activeID: {
				material:            material,
				active:              true,
				maxObjectGeneration: ^uint64(0),
			},
		},
	}
	return kr, nil
}

func validateKeyID(id string) error {
	if id == "" || len(id) > 64 {
		return fmt.Errorf("netsec: key ID must be 1..64 bytes")
	}
	return nil
}

// Grace returns the configured retirement grace period.
func (kr *KeyRing) Grace() time.Duration {
	kr.mu.RLock()
	defer kr.mu.RUnlock()
	return kr.grace
}

// SetGrace changes the grace period used by future rotations.
func (kr *KeyRing) SetGrace(grace time.Duration) error {
	if grace < 0 {
		return fmt.Errorf("netsec: negative key grace period")
	}
	kr.mu.Lock()
	kr.grace = grace
	kr.mu.Unlock()
	return nil
}

// ActiveKeyID returns the ID of the key used for new handles.
func (kr *KeyRing) ActiveKeyID() string {
	kr.mu.RLock()
	defer kr.mu.RUnlock()
	return kr.active
}

// KeyInfo returns metadata for keyID.
func (kr *KeyRing) KeyInfo(keyID string) (KeyInfo, bool) {
	kr.mu.RLock()
	defer kr.mu.RUnlock()
	e, ok := kr.keys[keyID]
	if !ok {
		return KeyInfo{}, false
	}
	return KeyInfo{
		ID:                  keyID,
		Active:              e.active,
		RetiredAt:           e.retiredAt,
		GraceUntil:          e.graceUntil,
		MaxObjectGeneration: e.maxObjectGeneration,
	}, true
}

// KeyIDs returns all key IDs in lexical order, active key first.
func (kr *KeyRing) KeyIDs() []string {
	kr.mu.RLock()
	defer kr.mu.RUnlock()
	ids := make([]string, 0, len(kr.keys))
	for id := range kr.keys {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if ids[i] == kr.active {
			return true
		}
		if ids[j] == kr.active {
			return false
		}
		return ids[i] < ids[j]
	})
	return ids
}

// Rotate installs newID as the active key and retires the previous active
// key.  cutoff must be the greatest object generation allocated before the
// rotation.  The retired key may verify only generations <= cutoff during the
// grace period; it can never authorize a newly created object.
func (kr *KeyRing) Rotate(newID string, secret []byte, now time.Time, cutoff uint64) error {
	if err := validateKeyID(newID); err != nil {
		return err
	}
	if len(secret) != 32 {
		return fmt.Errorf("netsec: AES-256 key must be 32 bytes, got %d", len(secret))
	}
	kr.mu.Lock()
	defer kr.mu.Unlock()
	if _, exists := kr.keys[newID]; exists {
		return deny(StatusInvalid, nil, "key ID %q already exists", newID)
	}
	old, ok := kr.keys[kr.active]
	if !ok {
		return fmt.Errorf("netsec: active key %q is missing", kr.active)
	}
	old.active = false
	old.retiredAt = now
	old.graceUntil = now.Add(kr.grace)
	old.maxObjectGeneration = cutoff
	var material [32]byte
	copy(material[:], secret)
	kr.keys[newID] = &keyEntry{
		material:            material,
		active:              true,
		maxObjectGeneration: ^uint64(0),
	}
	kr.active = newID
	return nil
}

func (kr *KeyRing) sealMaterial() (string, [32]byte, error) {
	kr.mu.RLock()
	defer kr.mu.RUnlock()
	e, ok := kr.keys[kr.active]
	if !ok {
		return "", [32]byte{}, fmt.Errorf("netsec: active key %q is missing", kr.active)
	}
	return kr.active, e.material, nil
}

func (kr *KeyRing) material(keyID string) ([32]byte, error) {
	kr.mu.RLock()
	defer kr.mu.RUnlock()
	e, ok := kr.keys[keyID]
	if !ok {
		return [32]byte{}, ErrUnknownKey
	}
	return e.material, nil
}

// authorize applies key-epoch rules.  An expired retired key is stale even if
// the object still exists; a retired key is never allowed to authorize an
// object generation newer than its cut-off.
func (kr *KeyRing) authorize(keyID string, objectGeneration uint64, now time.Time) error {
	kr.mu.RLock()
	defer kr.mu.RUnlock()
	e, ok := kr.keys[keyID]
	if !ok {
		return ErrUnknownKey
	}
	if e.active {
		return nil
	}
	if !now.Before(e.graceUntil) {
		return ErrKeyExpired
	}
	if objectGeneration > e.maxObjectGeneration {
		return ErrOldKeyNewObject
	}
	return nil
}

func serverTag(serverInstance string) [16]byte {
	sum := sha256.Sum256([]byte(serverInstance))
	var tag [16]byte
	copy(tag[:], sum[:16])
	return tag
}
