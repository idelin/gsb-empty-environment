package displaycommit

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash"
	"strings"
)

// Hash is the SHA-256 commitment type used throughout the package.
type Hash [32]byte

var ZeroHash Hash

// ParseHash parses an optional 0x-prefixed hexadecimal hash.
func ParseHash(s string) (Hash, error) {
	var h Hash
	s = strings.TrimPrefix(strings.TrimSpace(s), "0x")
	s = strings.TrimPrefix(s, "0X")
	if len(s) != hex.EncodedLen(len(h)) {
		return h, errors.New("displaycommit: hash must be 32 bytes")
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return h, err
	}
	copy(h[:], b)
	return h, nil
}

func (h Hash) String() string {
	return "0x" + hex.EncodeToString(h[:])
}

func (h Hash) IsZero() bool {
	return h == ZeroHash
}

// MarshalText implements encoding.TextMarshaler.
func (h Hash) MarshalText() ([]byte, error) {
	return []byte(h.String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (h *Hash) UnmarshalText(text []byte) error {
	parsed, err := ParseHash(string(text))
	if err != nil {
		return err
	}
	*h = parsed
	return nil
}

func writeLenPrefixed(h hash.Hash, b []byte) {
	var n [binary.MaxVarintLen64]byte
	m := binary.PutUvarint(n[:], uint64(len(b)))
	_, _ = h.Write(n[:m])
	_, _ = h.Write(b)
}

// DomainHash computes a length-prefixed, domain-separated SHA-256 hash.
func DomainHash(domain string, parts ...[]byte) Hash {
	h := sha256.New()
	writeLenPrefixed(h, []byte(domain))
	for _, part := range parts {
		writeLenPrefixed(h, part)
	}
	var out Hash
	copy(out[:], h.Sum(nil))
	return out
}

// Sum256 is a small convenience wrapper around crypto/sha256.
func Sum256(b []byte) Hash {
	return sha256.Sum256(b)
}

// CommitRaw commits a leaf's normalized wire type and raw value bytes.
func CommitRaw(wire any, raw []byte) Hash {
	return DomainHash("displaycommit/raw-value/v1", []byte(NormalizeWire(wire)), raw)
}

func uint64Bytes(v uint64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	return b[:]
}

func int64Bytes(v int64) []byte {
	return uint64Bytes(uint64(v))
}
