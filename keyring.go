package netsec

import (
	"fmt"
)

// KeyGeneration identifies which key table supplied a key.
type KeyGeneration uint8

const (
	// KeyGenerationNone is used for identities that do not use a key, such as
	// the null scheme.
	KeyGenerationNone KeyGeneration = iota
	// KeyGenerationPrevious identifies the previous generation key table.
	KeyGenerationPrevious
	// KeyGenerationCurrent identifies the current generation key table.
	KeyGenerationCurrent
)

func (g KeyGeneration) String() string {
	switch g {
	case KeyGenerationNone:
		return "none"
	case KeyGenerationPrevious:
		return "previous"
	case KeyGenerationCurrent:
		return "current"
	default:
		return fmt.Sprintf("generation-%d", uint8(g))
	}
}

// KeyRing holds the current and previous generation home-network private
// key tables. Keys are addressed by the public key identifier carried in the
// SUCI. The current table wins if the same identifier exists in both tables.
type KeyRing struct {
	current  map[int][]byte
	previous map[int][]byte
}

// NewKeyRing copies the supplied key tables and validates their entries. The
// key material is treated as opaque by this package and is only handed to the
// injected Decryptor.
func NewKeyRing(current, previous map[int][]byte) (*KeyRing, error) {
	cur, err := copyKeyTable(current)
	if err != nil {
		return nil, err
	}
	prev, err := copyKeyTable(previous)
	if err != nil {
		return nil, err
	}
	return &KeyRing{current: cur, previous: prev}, nil
}

func copyKeyTable(table map[int][]byte) (map[int][]byte, error) {
	copied := make(map[int][]byte, len(table))
	for id, key := range table {
		if id < 1 || id > 255 {
			return nil, fmt.Errorf("%w: got %d", ErrInvalidKeyID, id)
		}
		if len(key) == 0 {
			return nil, fmt.Errorf("%w: id %d", ErrEmptyKeyMaterial, id)
		}
		copied[id] = cloneBytes(key)
	}
	return copied, nil
}

// Lookup finds a key by public key identifier in the current table first and
// then in the previous table. It returns a copy of the key material.
func (k *KeyRing) Lookup(id int) ([]byte, KeyGeneration, bool) {
	if k == nil {
		return nil, KeyGenerationNone, false
	}
	if key, ok := k.current[id]; ok {
		return cloneBytes(key), KeyGenerationCurrent, true
	}
	if key, ok := k.previous[id]; ok {
		return cloneBytes(key), KeyGenerationPrevious, true
	}
	return nil, KeyGenerationNone, false
}

// Has reports whether the identifier exists in either generation.
func (k *KeyRing) Has(id int) bool {
	_, _, ok := k.Lookup(id)
	return ok
}
