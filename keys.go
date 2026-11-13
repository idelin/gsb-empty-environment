package netsec

import "fmt"

// KeySize is the exact size, in bytes, of a raw envelope key.
const KeySize = 32

// Key is a stable key identifier together with its 32-byte raw key.
//
// Key identifiers are scoped by the host's key ring and are never secret.
// The same identifier must always refer to the same key material for the
// lifetime of stored records.
type Key struct {
	ID       string
	Material [KeySize]byte
}

// NewKey validates and copies raw key material into a Key.
func NewKey(id string, material []byte) (Key, error) {
	if err := validateKeyID(id); err != nil {
		return Key{}, err
	}
	if len(material) != KeySize {
		return Key{}, fmt.Errorf("%w: got %d bytes, want %d", ErrInvalidKey, len(material), KeySize)
	}

	var key Key
	key.ID = id
	copy(key.Material[:], material)
	return key, nil
}

// KeyRing is a read-only, host-provided key lookup interface.
//
// LookupKey must return ok == true only when keyID resolves to exactly one
// key entry. It must return ok == false for an unknown identifier and for an
// identifier with duplicate entries. Implementations must not mutate their
// key material as a result of a lookup.
//
// The envelope treats the returned bytes as read-only and makes its own copy.
// Returning a defensive copy is nevertheless recommended.
type KeyRing interface {
	LookupKey(keyID string) (key []byte, ok bool)
}

// StaticKeyRing is an immutable key ring suitable for hosts that load their
// keys once at startup. Duplicate identifiers are preserved rather than
// collapsed: a lookup for a duplicated identifier reports that no unique key
// exists, and envelope opening fails.
type StaticKeyRing struct {
	entries map[string][][]byte
}

// NewStaticKeyRing validates keys and builds an immutable ring. Duplicate key
// identifiers are accepted but will never resolve to a unique key. Invalid
// identifiers or key lengths are rejected.
func NewStaticKeyRing(keys ...Key) (*StaticKeyRing, error) {
	ring := &StaticKeyRing{entries: make(map[string][][]byte, len(keys))}
	for i, key := range keys {
		if err := validateKeyID(key.ID); err != nil {
			return nil, fmt.Errorf("key %d: %w", i, err)
		}
		material := make([]byte, KeySize)
		copy(material, key.Material[:])
		ring.entries[key.ID] = append(ring.entries[key.ID], material)
	}
	return ring, nil
}

// LookupKey implements KeyRing.
func (r *StaticKeyRing) LookupKey(keyID string) ([]byte, bool) {
	if r == nil {
		return nil, false
	}
	materials := r.entries[keyID]
	if len(materials) != 1 {
		return nil, false
	}
	key := make([]byte, KeySize)
	copy(key, materials[0])
	return key, true
}

func validateKeyID(id string) error {
	if len(id) == 0 {
		return fmt.Errorf("%w: empty key identifier", ErrInvalidKeyID)
	}
	if len(id) > MaxKeyIDLen {
		return fmt.Errorf("%w: %d bytes exceeds %d", ErrInvalidKeyID, len(id), MaxKeyIDLen)
	}
	return nil
}
