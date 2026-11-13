package netsec

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// Digest is a SHA-256 digest.
type Digest [sha256.Size]byte

// SumDigest returns the SHA-256 digest of b.
func SumDigest(b []byte) Digest {
	return Digest(sha256.Sum256(b))
}

// ParseDigest parses a 64-character hexadecimal SHA-256 digest.
func ParseDigest(s string) (Digest, error) {
	var d Digest
	if len(s) != hex.EncodedLen(len(d)) {
		return d, fmt.Errorf("netsec: SHA-256 digest must contain %d hex characters", hex.EncodedLen(len(d)))
	}
	if _, err := hex.Decode(d[:], []byte(s)); err != nil {
		return Digest{}, fmt.Errorf("netsec: invalid SHA-256 digest: %w", err)
	}
	return d, nil
}

// String returns the lower-case hexadecimal form of d.
func (d Digest) String() string {
	return hex.EncodeToString(d[:])
}

// IsZero reports whether d is the all-zero digest.
func (d Digest) IsZero() bool {
	return d == Digest{}
}

// MarshalText encodes d as lower-case hexadecimal.
func (d Digest) MarshalText() ([]byte, error) {
	return []byte(d.String()), nil
}

// UnmarshalText parses a 64-character hexadecimal SHA-256 digest.
func (d *Digest) UnmarshalText(text []byte) error {
	parsed, err := ParseDigest(string(text))
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}
