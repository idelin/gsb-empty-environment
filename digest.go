package netsec

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
)

// DigestSize is the length in bytes of a Digest.
const DigestSize = sha256.Size

// Digest is a content digest that pins an entry to the concrete input it was
// extracted from. The zero Digest is invalid.
type Digest [DigestSize]byte

// SumDigest returns the digest of data.
func SumDigest(data []byte) Digest {
	return sha256.Sum256(data)
}

// IsZero reports whether d is the zero digest.
func (d Digest) IsZero() bool {
	return d == Digest{}
}

// Hex returns the lowercase hexadecimal encoding of d.
func (d Digest) Hex() string {
	return hex.EncodeToString(d[:])
}

func (d Digest) String() string {
	return d.Hex()
}

// Compare returns -1, 0 or 1 depending on whether d sorts before, equal to or
// after other. Byte ordering is used so snapshot ordering is deterministic.
func (d Digest) Compare(other Digest) int {
	return bytes.Compare(d[:], other[:])
}
