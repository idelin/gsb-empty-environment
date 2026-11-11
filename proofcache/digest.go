package proofcache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
)

// Digest is a domain-separated SHA-256 content digest.
//
// A digest is always produced through [Hasher] so that structurally different
// inputs (for example an IR blob and a rule identifier) can never collide by
// being concatenated naively. The zero value is a valid digest of the empty
// hash state and is never returned by a finished [Hasher].
type Digest [sha256.Size]byte

// ParseDigest decodes a hex-encoded digest.
func ParseDigest(s string) (Digest, error) {
	var d Digest
	raw, err := hex.DecodeString(s)
	if err != nil {
		return Digest{}, fmt.Errorf("%w: %v", ErrBadDigest, err)
	}
	if len(raw) != len(d) {
		return Digest{}, ErrBadDigest
	}
	copy(d[:], raw)
	return d, nil
}

// String renders the digest as lowercase hex.
func (d Digest) String() string {
	return hex.EncodeToString(d[:])
}

// IsZero reports whether d is the zero digest.
func (d Digest) IsZero() bool {
	return d == Digest{}
}

// Less gives digests a deterministic total order. It exists so callers can
// keep digest-keyed sets stable across runs.
func (d Digest) Less(other Digest) bool {
	for i := range d {
		if d[i] != other[i] {
			return d[i] < other[i]
		}
	}
	return false
}

// Hasher builds a [Digest] with explicit domain separation.
//
// Every section written through a tag-prefixed Write is encoded as
// tag || uint64(length) || payload, so concatenated sections cannot be
// ambiguous. A typical binding binds several independently sourced blobs:
//
//	h := NewHasher()
//	h.WriteString("ir", ir)
//	h.WriteString("rule", ruleID)
//	fp := h.Sum()
type Hasher struct {
	h hash.Hash
}

// NewHasher starts a domain-separated hash.
func NewHasher() *Hasher {
	return &Hasher{h: sha256.New()}
}

// WriteString records a length-prefixed tagged section.
func (x *Hasher) WriteString(tag, payload string) {
	x.writeTag(tag)
	var n [8]byte
	putUint64(n[:], uint64(len(payload)))
	x.h.Write(n[:])
	x.h.Write([]byte(payload))
}

// WriteDigest records a fixed-width tagged section containing another digest.
func (x *Hasher) WriteDigest(tag string, d Digest) {
	x.writeTag(tag)
	var n [8]byte
	putUint64(n[:], uint64(len(d)))
	x.h.Write(n[:])
	x.h.Write(d[:])
}

// WriteJSON records a tagged section containing the canonical JSON encoding
// of v. It is intended for structured metadata such as rule versions.
func (x *Hasher) WriteJSON(tag string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	x.writeTag(tag)
	var n [8]byte
	putUint64(n[:], uint64(len(raw)))
	x.h.Write(n[:])
	x.h.Write(raw)
	return nil
}

// Sum finalizes and returns the digest. The hasher must not be reused after
// Sum; callers create a fresh [Hasher] per binding.
func (x *Hasher) Sum() Digest {
	var out Digest
	sum := x.h.Sum(nil)
	copy(out[:], sum)
	return out
}

func (x *Hasher) writeTag(tag string) {
	var n [8]byte
	putUint64(n[:], uint64(len(tag)))
	x.h.Write(n[:])
	x.h.Write([]byte(tag))
}

func putUint64(b []byte, v uint64) {
	b[0] = byte(v >> 56)
	b[1] = byte(v >> 48)
	b[2] = byte(v >> 40)
	b[3] = byte(v >> 32)
	b[4] = byte(v >> 24)
	b[5] = byte(v >> 16)
	b[6] = byte(v >> 8)
	b[7] = byte(v)
}
