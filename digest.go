package pagezip

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

// Digest is the content address of one dictionary version.
type Digest [sha256.Size]byte

// DigestOf hashes dictionary content into its version digest.
func DigestOf(content []byte) Digest {
	return Digest(sha256.Sum256(content))
}

func (d Digest) String() string {
	return hex.EncodeToString(d[:])
}

var errBadDigest = errors.New("pagezip: invalid digest encoding")

// ParseDigest parses a hex-encoded digest.
func ParseDigest(s string) (Digest, error) {
	var d Digest
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != sha256.Size {
		return d, errBadDigest
	}
	copy(d[:], b)
	return d, nil
}
