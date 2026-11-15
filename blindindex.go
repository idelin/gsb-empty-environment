package netsec

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"strconv"
)

// Generation identifies one key generation and is part of every blind index.
// Generation zero is invalid.
type Generation uint64

// Version is an alias for Generation. It is the version domain included in the
// HMAC input and is kept separate from tenant, collection, field, and value.
type Version = Generation

// KeyHandle is supplied by the host. The host remains responsible for storing
// and rotating key material; this package neither caches nor persists keys.
//
// Key must return the key for the requested generation. A returned slice is
// consumed immediately and is not retained or exposed by this package.
type KeyHandle interface {
	Key(ctx context.Context, generation Generation) ([]byte, error)
}

// KeyProvider is an alias for KeyHandle.
type KeyProvider = KeyHandle

// KeyFunc adapts a function to KeyHandle.
type KeyFunc func(ctx context.Context, generation Generation) ([]byte, error)

// Key implements KeyHandle.
func (f KeyFunc) Key(ctx context.Context, generation Generation) ([]byte, error) {
	return f(ctx, generation)
}

// Scope identifies the tenant, collection, and field that own a blind index.
// All three components must be non-empty. They are not trimmed or normalized
// implicitly because doing so would merge distinct storage namespaces.
type Scope struct {
	Tenant     string
	Collection string
	Field      string
}

// Validate checks that every scope domain is present.
func (s Scope) Validate() error {
	if s.Tenant == "" {
		return ErrEmptyTenant
	}
	if s.Collection == "" {
		return ErrEmptyCollection
	}
	if s.Field == "" {
		return ErrEmptyField
	}
	return nil
}

// Index is the HMAC-SHA256 blind index for one generation and scope/value.
// The digest alone is not enough to query safely; Generation must match an
// active generation.
type Index struct {
	Generation Generation
	Digest     [sha256.Size]byte
}

// Bytes returns a copy of the digest.
func (i Index) Bytes() []byte {
	sum := make([]byte, len(i.Digest))
	copy(sum, i.Digest[:])
	return sum
}

// Hex returns the lowercase hexadecimal digest without the generation.
func (i Index) Hex() string {
	return hex.EncodeToString(i.Digest[:])
}

// String returns the generation and digest in a diagnostic-safe form. It never
// contains the indexed value or key material.
func (i Index) String() string {
	return "generation=" + strconv.FormatUint(uint64(i.Generation), 10) + " index=" + i.Hex()
}

// Equal compares two indexes.
func (i Index) Equal(other Index) bool {
	return i.Generation == other.Generation &&
		subtle.ConstantTimeCompare(i.Digest[:], other.Digest[:]) == 1
}

// EncodeComponents returns the unambiguous HMAC input for an index:
//
//	LP(decimal version) || LP(tenant) || LP(collection) || LP(field) || LP(value)
//
// LP is an unsigned 64-bit big-endian byte length followed by that many bytes.
// The value component may be empty; all other components must be non-empty.
func EncodeComponents(generation Generation, scope Scope, value []byte) ([]byte, error) {
	if generation == 0 {
		return nil, ErrInvalidGeneration
	}
	if err := scope.Validate(); err != nil {
		return nil, err
	}

	version := strconv.FormatUint(uint64(generation), 10)
	encoded := make([]byte, 0, 5*8+len(version)+len(scope.Tenant)+len(scope.Collection)+len(scope.Field)+len(value))
	encoded = appendLengthPrefixed(encoded, []byte(version))
	encoded = appendLengthPrefixed(encoded, []byte(scope.Tenant))
	encoded = appendLengthPrefixed(encoded, []byte(scope.Collection))
	encoded = appendLengthPrefixed(encoded, []byte(scope.Field))
	encoded = appendLengthPrefixed(encoded, value)
	return encoded, nil
}

func appendLengthPrefixed(dst, component []byte) []byte {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(component)))
	dst = append(dst, size[:]...)
	dst = append(dst, component...)
	return dst
}

func deriveIndex(key []byte, generation Generation, scope Scope, value []byte) (Index, error) {
	encoded, err := EncodeComponents(generation, scope, value)
	if err != nil {
		return Index{}, err
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(encoded)

	var digest [sha256.Size]byte
	copy(digest[:], mac.Sum(nil))
	return Index{Generation: generation, Digest: digest}, nil
}
