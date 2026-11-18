package content

import (
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash"
	"strings"
)

// Algorithm is a supported content digest algorithm.
type Algorithm string

const (
	SHA256 Algorithm = "sha256"
	SHA512 Algorithm = "sha512"
)

// Digest identifies content by algorithm and hex-encoded hash.
type Digest struct {
	Algo Algorithm
	Hex  string
}

// ParseDigest parses "algo:hex" digests. Only sha256 and sha512 are
// accepted, and the hex part must match the algorithm's digest length.
func ParseDigest(s string) (Digest, error) {
	algo, hexPart, ok := strings.Cut(s, ":")
	if !ok || algo == "" || hexPart == "" {
		return Digest{}, fmt.Errorf("digest %q: want \"algo:hex\"", s)
	}
	var want int
	switch Algorithm(algo) {
	case SHA256:
		want = sha256.Size * 2
	case SHA512:
		want = sha512.Size * 2
	default:
		return Digest{}, fmt.Errorf("digest %q: unsupported algorithm %q", s, algo)
	}
	if len(hexPart) != want {
		return Digest{}, fmt.Errorf("digest %q: %s hex part must be %d chars, got %d", s, algo, want, len(hexPart))
	}
	if _, err := hex.DecodeString(hexPart); err != nil {
		return Digest{}, fmt.Errorf("digest %q: invalid hex: %w", s, err)
	}
	return Digest{Algo: Algorithm(algo), Hex: strings.ToLower(hexPart)}, nil
}

func (d Digest) String() string { return string(d.Algo) + ":" + d.Hex }

func (d Digest) newHasher() hash.Hash {
	if d.Algo == SHA512 {
		return sha512.New()
	}
	return sha256.New()
}
