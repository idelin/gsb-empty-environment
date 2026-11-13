package netsec

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// SecurityDescriptor is an immutable, opaque security descriptor blob.
type SecurityDescriptor string

// Digest is the SHA-256 digest of a security descriptor.
type Digest [sha256.Size]byte

// HashDescriptor returns the digest a policy uses to bind a descriptor.
func HashDescriptor(sd SecurityDescriptor) Digest {
	return Digest(sha256.Sum256([]byte(sd)))
}

// IsZero reports whether d is the zero digest.
func (d Digest) IsZero() bool {
	return d == Digest{}
}

// String returns the lower-case hexadecimal digest.
func (d Digest) String() string {
	return hex.EncodeToString(d[:])
}

// Bytes returns a copy of the digest bytes.
func (d Digest) Bytes() []byte {
	out := make([]byte, len(d))
	copy(out, d[:])
	return out
}

// ParseDigest parses a 64-character hexadecimal SHA-256 digest.
func ParseDigest(s string) (Digest, error) {
	var d Digest
	if len(s) != hex.EncodedLen(len(d)) {
		return d, fmt.Errorf("%w: digest must be %d hex characters", ErrInvalidDigest, hex.EncodedLen(len(d)))
	}
	if _, err := hex.Decode(d[:], []byte(s)); err != nil {
		return Digest{}, fmt.Errorf("%w: %v", ErrInvalidDigest, err)
	}
	return d, nil
}
