package netsec

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
)

// Hash is a 32-byte SHA-256 digest used for every commitment in this package.
type Hash [32]byte

// ZeroHash is the all-zero digest. Valid block, transaction, event, and Merkle
// commitments are never allowed to be zero.
var ZeroHash Hash

const hashDomain = "example.org/netsec/q0453"

// String returns the lowercase hexadecimal representation of h.
func (h Hash) String() string {
	return hex.EncodeToString(h[:])
}

// IsZero reports whether h is the all-zero digest.
func (h Hash) IsZero() bool {
	return h == ZeroHash
}

// MarshalText implements encoding.TextMarshaler.
func (h Hash) MarshalText() ([]byte, error) {
	return []byte(h.String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (h *Hash) UnmarshalText(text []byte) error {
	if h == nil {
		return errors.New("netsec: nil Hash receiver")
	}
	if len(text) != hex.EncodedLen(len(h)) {
		return fmt.Errorf("netsec: hash must be %d hex characters", hex.EncodedLen(len(h)))
	}
	var decoded Hash
	if _, err := hex.Decode(decoded[:], text); err != nil {
		return fmt.Errorf("netsec: invalid hash: %w", err)
	}
	*h = decoded
	return nil
}

// ParseHash parses a hexadecimal 32-byte hash.
func ParseHash(s string) (Hash, error) {
	var h Hash
	if err := h.UnmarshalText([]byte(s)); err != nil {
		return Hash{}, err
	}
	return h, nil
}

func writeBytes(h hash.Hash, b []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(b)))
	_, _ = h.Write(size[:])
	_, _ = h.Write(b)
}

func writeString(h hash.Hash, s string) {
	writeBytes(h, []byte(s))
}

func writeUint32(h hash.Hash, v uint32) {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], v)
	_, _ = h.Write(buf[:])
}

func writeUint64(h hash.Hash, v uint64) {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], v)
	_, _ = h.Write(buf[:])
}

func writeInt64(h hash.Hash, v int64) {
	writeUint64(h, uint64(v))
}

// hashDomainSeparated hashes a package-level domain, a type/version domain,
// and a sequence of length-prefixed byte strings.
func hashDomainSeparated(domain string, parts ...[]byte) Hash {
	h := sha256.New()
	writeString(h, hashDomain)
	writeString(h, domain)
	for _, part := range parts {
		writeBytes(h, part)
	}
	var out Hash
	copy(out[:], h.Sum(nil))
	return out
}

// sumParts is a small convenience used by tests and internal commitments.
func sumParts(domain string, parts ...[]byte) Hash {
	return hashDomainSeparated(domain, parts...)
}
