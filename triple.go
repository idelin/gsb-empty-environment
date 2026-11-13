package netsec

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
)

// Triple is one Beaver triple. A, B, and C each hold three additive shares.
// The unique ID is bound to the shares by TripleDigest and by the persistent
// triple state, so a caller cannot check one set of shares and later consume a
// different set under the same ID.
type Triple struct {
	ID string
	A  AdditiveShares
	B  AdditiveShares
	C  AdditiveShares
}

// Validate checks the ID and every share of every component.
func (f Field) ValidateTriple(t Triple) error {
	if t.ID == "" {
		return fmt.Errorf("%w: empty ID", ErrTripleID)
	}
	if len(t.ID) > 4096 {
		return fmt.Errorf("%w: ID too long", ErrTripleID)
	}
	if err := f.ValidateShares(t.A); err != nil {
		return fmt.Errorf("%w: A: %v", ErrInvalidTriple, err)
	}
	if err := f.ValidateShares(t.B); err != nil {
		return fmt.Errorf("%w: B: %v", ErrInvalidTriple, err)
	}
	if err := f.ValidateShares(t.C); err != nil {
		return fmt.Errorf("%w: C: %v", ErrInvalidTriple, err)
	}
	return nil
}

// TripleDigest binds a triple ID to canonical shares and the field modulus.
func TripleDigest(f Field, t Triple) [sha256.Size]byte {
	h := sha256.New()
	h.Write([]byte("example.org/netsec/q0106/triple\x00"))
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], f.P)
	h.Write(buf[:])
	writeLengthPrefixedBuffer(h, []byte(t.ID))
	for _, shares := range [3]AdditiveShares{t.A, t.B, t.C} {
		for _, s := range shares {
			binary.BigEndian.PutUint32(buf[:], s)
			h.Write(buf[:])
		}
	}
	var out [sha256.Size]byte
	copy(out[:], h.Sum(nil))
	return out
}

// OrderedPair returns the canonical (candidate, check) pair and rejects reuse
// of the same ID in both roles.
func OrderedPair(candidate, check Triple) (TriplePair, error) {
	if candidate.ID == "" || check.ID == "" {
		return TriplePair{}, fmt.Errorf("%w: empty ID", ErrTripleID)
	}
	if candidate.ID == check.ID {
		return TriplePair{}, fmt.Errorf("%w: candidate and check IDs are equal", ErrTripleID)
	}
	return TriplePair{First: candidate.ID, Second: check.ID}, nil
}
