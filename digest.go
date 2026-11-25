package evidence

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// Digest is a SHA-256 digest.  Public-key digests, proof digests and identity
// list digests use this representation.
type Digest [sha256.Size]byte

// DigestBytes returns the SHA-256 digest of b.
func DigestBytes(b []byte) Digest {
	return Digest(sha256.Sum256(b))
}

// DigestOf hashes the concatenation of the supplied byte slices.
func DigestOf(parts ...[]byte) Digest {
	h := sha256.New()
	for _, p := range parts {
		_, _ = h.Write(p)
	}
	var d Digest
	copy(d[:], h.Sum(nil))
	return d
}

// ParseDigest parses 64 hexadecimal characters, optionally prefixed with
// "sha256:".
func ParseDigest(s string) (Digest, error) {
	var d Digest
	orig := s
	s = strings.TrimSpace(s)
	if len(s) >= len("sha256:") && strings.EqualFold(s[:len("sha256:")], "sha256:") {
		s = s[len("sha256:"):]
	}
	if len(s) != hex.EncodedLen(len(d)) {
		return d, fmt.Errorf("%w: want 32 bytes, got %q", ErrInvalidDigest, orig)
	}
	if _, err := hex.Decode(d[:], []byte(s)); err != nil {
		return Digest{}, fmt.Errorf("%w: %v", ErrInvalidDigest, err)
	}
	return d, nil
}

// MustParseDigest is ParseDigest with a panic on malformed input.
func MustParseDigest(s string) Digest {
	d, err := ParseDigest(s)
	if err != nil {
		panic(err)
	}
	return d
}

// IsZero reports whether d is the all-zero digest.
func (d Digest) IsZero() bool {
	var zero Digest
	return d == zero
}

// Hex returns the lowercase hexadecimal encoding of d.
func (d Digest) Hex() string { return hex.EncodeToString(d[:]) }

func (d Digest) String() string { return d.Hex() }

// MarshalText implements encoding.TextMarshaler.
func (d Digest) MarshalText() ([]byte, error) {
	return []byte(d.Hex()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (d *Digest) UnmarshalText(text []byte) error {
	parsed, err := ParseDigest(string(text))
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}
