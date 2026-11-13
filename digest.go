package netsec

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

// Digest is a SHA-256 digest.  Digests in this package are domain separated
// and length prefixed so that concatenating untrusted fields cannot produce
// the same digest as a different tuple of fields.
type Digest [sha256.Size]byte

// ZeroDigest is the all-zero digest.  It is used for "no value" (for
// example, a metadata-only operation has a zero DataDigest).
var ZeroDigest Digest

// DigestBytes returns the SHA-256 digest of b.
func DigestBytes(b []byte) Digest {
	return sha256.Sum256(b)
}

// DigestString returns the SHA-256 digest of the UTF-8 encoding of s.
func DigestString(s string) Digest {
	return DigestBytes([]byte(s))
}

// ParseDigest parses a lower-case or upper-case hexadecimal SHA-256 digest.
func ParseDigest(s string) (Digest, error) {
	var d Digest
	b, err := hex.DecodeString(s)
	if err != nil {
		return d, fmt.Errorf("netsec: parse digest: %w", err)
	}
	if len(b) != len(d) {
		return d, fmt.Errorf("netsec: parse digest: got %d bytes, want %d", len(b), len(d))
	}
	copy(d[:], b)
	return d, nil
}

func MustParseDigest(s string) Digest {
	d, err := ParseDigest(s)
	if err != nil {
		panic(err)
	}
	return d
}

func (d Digest) String() string {
	return hex.EncodeToString(d[:])
}

func (d Digest) IsZero() bool {
	return d == ZeroDigest
}

func (d Digest) MarshalText() ([]byte, error) {
	return []byte(d.String()), nil
}

func (d *Digest) UnmarshalText(text []byte) error {
	parsed, err := ParseDigest(string(text))
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}

func (d Digest) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

func (d *Digest) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	return d.UnmarshalText([]byte(s))
}

func hashParts(domain string, parts ...[]byte) Digest {
	h := sha256.New()
	writePart := func(p []byte) {
		var n [8]byte
		for i := 0; i < 8; i++ {
			n[7-i] = byte(uint64(len(p)) >> (8 * i))
		}
		_, _ = h.Write(n[:])
		_, _ = h.Write(p)
	}
	writePart([]byte(domain))
	for _, p := range parts {
		writePart(p)
	}
	var out Digest
	copy(out[:], h.Sum(nil))
	return out
}

// canonicalJSON is deterministic for the value types used in this package:
// struct fields are emitted in declaration order and encoding/json sorts map
// keys.
func canonicalJSON(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func canonicalHash(domain string, v any) (Digest, error) {
	b, err := canonicalJSON(v)
	if err != nil {
		return ZeroDigest, err
	}
	return hashParts(domain, b), nil
}

// ErrInvalidDigest is returned when a digest or signed value is malformed.
var ErrInvalidDigest = errors.New("netsec: invalid digest")
