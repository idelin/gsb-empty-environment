package netsec

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash"
	"io"
)

// Version identifies one key generation. Versions are part of the authenticated
// index input and must not be empty.
type Version string

// Index is a blind index bound to a single key-generation version. It contains
// no plaintext, tenant, collection, or field metadata.
type Index struct {
	Version Version
	Digest  [sha256.Size]byte
}

// Bytes returns a copy of the digest.
func (idx Index) Bytes() []byte {
	out := make([]byte, len(idx.Digest))
	copy(out, idx.Digest[:])
	return out
}

// Hex returns the lower-case hexadecimal digest.
func (idx Index) Hex() string {
	return hex.EncodeToString(idx.Digest[:])
}

// String returns a diagnostic form that contains only the generation and the
// digest. It never contains an indexed value or other input metadata.
func (idx Index) String() string {
	return string(idx.Version) + ":" + idx.Hex()
}

// Equal reports whether two indexes are identical.
func (idx Index) Equal(other Index) bool {
	return idx.Version == other.Version &&
		subtle.ConstantTimeCompare(idx.Digest[:], other.Digest[:]) == 1
}

// KeyHandle supplies key material for a generation. The returned slice must be
// non-empty. It should be at least 32 bytes; the package deliberately does not
// shorten or derive host-managed keys.
type KeyHandle interface {
	Key(version Version) ([]byte, error)
}

// KeyFunc adapts a function to KeyHandle.
type KeyFunc func(version Version) ([]byte, error)

// Key implements KeyHandle.
func (fn KeyFunc) Key(version Version) ([]byte, error) {
	return fn(version)
}

// Indexer computes blind indexes with keys obtained from a host KeyHandle.
type Indexer struct {
	keys KeyHandle
}

// NewIndexer creates an indexer backed by keys.
func NewIndexer(keys KeyHandle) (*Indexer, error) {
	if keys == nil {
		return nil, ErrNilKeyHandle
	}
	return &Indexer{keys: keys}, nil
}

// Index computes HMAC-SHA256(version, tenant, collection, field, value).
//
// The five inputs are encoded as fixed-order, 8-byte big-endian length-prefixed
// byte strings. The version, tenant, collection, and field must be non-empty;
// value may be empty.
func (ix *Indexer) Index(version Version, tenant, collection, field, value string) (Index, error) {
	if err := validateIndexDomains(version, tenant, collection, field); err != nil {
		return Index{}, err
	}
	if ix == nil || ix.keys == nil {
		return Index{}, ErrNilKeyHandle
	}

	key, err := ix.keys.Key(version)
	if err != nil {
		return Index{}, err
	}
	if len(key) == 0 {
		return Index{}, ErrInvalidKey
	}

	mac := hmac.New(sha256.New, key)
	writeComponent(mac, string(version))
	writeComponent(mac, tenant)
	writeComponent(mac, collection)
	writeComponent(mac, field)
	writeComponent(mac, value)

	var result Index
	result.Version = version
	copy(result.Digest[:], mac.Sum(nil))
	return result, nil
}

var (
	// ErrEmptyDomain is returned when version, tenant, collection, or field is
	// empty. Value is intentionally allowed to be empty.
	ErrEmptyDomain = errors.New("netsec: blind-index version, tenant, collection, and field must be non-empty")
	// ErrNilKeyHandle is returned when no host key handle is configured.
	ErrNilKeyHandle = errors.New("netsec: nil key handle")
	// ErrInvalidKey is returned when the host key handle supplies no key.
	ErrInvalidKey = errors.New("netsec: key handle returned an empty key")
)

func validateIndexDomains(version Version, tenant, collection, field string) error {
	if version == "" || tenant == "" || collection == "" || field == "" {
		return ErrEmptyDomain
	}
	return nil
}

// writeComponent writes one unambiguously framed component. All components use
// the same big-endian 64-bit length prefix, including empty values.
func writeComponent(dst hash.Hash, component string) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(component)))
	_, _ = dst.Write(size[:])
	_, _ = io.WriteString(dst, component)
}
