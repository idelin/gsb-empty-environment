package netsec

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// DigestSize is the length in bytes of a SHA-256 witness digest.
const DigestSize = sha256.Size

// Digest is a content-addressed SHA-256 identifier.
type Digest [DigestSize]byte

// ZeroDigest is the all-zero digest used for the root (first) proof.
var ZeroDigest Digest

// Hex renders the digest as lowercase hexadecimal text.
func (d Digest) Hex() string {
	return hex.EncodeToString(d[:])
}

// IsZero reports whether the digest is the zero value.
func (d Digest) IsZero() bool {
	return d == ZeroDigest
}

// String implements fmt.Stringer.
func (d Digest) String() string {
	return d.Hex()
}

// ParseDigest decodes a hexadecimal digest.
func ParseDigest(s string) (Digest, error) {
	var out Digest
	raw, err := hex.DecodeString(s)
	if err != nil {
		return out, fmt.Errorf("parse digest: %w", err)
	}
	if len(raw) != DigestSize {
		return out, fmt.Errorf("parse digest: got %d bytes, want %d", len(raw), DigestSize)
	}
	copy(out[:], raw)
	return out, nil
}

// SumDigest hashes arbitrary data into a Digest.
func SumDigest(data []byte) Digest {
	return sha256.Sum256(data)
}
