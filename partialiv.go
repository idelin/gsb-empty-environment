package netsec

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
)

// IVSpec describes the initialization vector layout required by a concrete
// authenticated encryption algorithm.
//
// An IV is the concatenation of a fixed Prefix (for example a per-key salt or
// device identifier) and a per-message PartialIV drawn as fresh randomness:
//
//	IV = Prefix || PartialIV
//
// The full IV length is therefore PrefixLen + PartialIVLen bytes.
type IVSpec struct {
	// Algorithm is a human-readable name of the algorithm this spec belongs to.
	Algorithm string

	// PrefixLen is the required length in bytes of the fixed prefix.
	PrefixLen int

	// PartialIVLen is the required length in bytes of the per-message partial IV.
	PartialIVLen int
}

// AESGCM96 is the standard AES-GCM configuration whose 96-bit (12-byte) nonce
// is split into an 8-byte fixed prefix and a 4-byte random partial IV.
var AESGCM96 = IVSpec{
	Algorithm:    "AES-GCM-96",
	PrefixLen:    8,
	PartialIVLen: 4,
}

var (
	// ErrBadPartialIVLen is returned when a partial IV does not have exactly the
	// length the algorithm requires. It is never silently padded.
	ErrBadPartialIVLen = errors.New("netsec: partial IV has invalid length")

	// ErrBadPrefixLen is returned when the fixed prefix has an unexpected length.
	ErrBadPrefixLen = errors.New("netsec: prefix has invalid length")
)

// IVLen reports the total IV length in bytes mandated by the spec.
func (s IVSpec) IVLen() int {
	return s.PrefixLen + s.PartialIVLen
}

// ValidatePartialIV reports whether partial has exactly the length the
// algorithm requires. A partial IV that is too short is NOT left-padded with
// zeros: padding would let distinct messages collapse onto the same IV.
// Instead, any length mismatch fails with ErrBadPartialIVLen.
func (s IVSpec) ValidatePartialIV(partial []byte) error {
	if len(partial) != s.PartialIVLen {
		return fmt.Errorf("%w: %s requires %d bytes, got %d",
			ErrBadPartialIVLen, s.Algorithm, s.PartialIVLen, len(partial))
	}
	return nil
}

func (s IVSpec) validatePrefix(prefix []byte) error {
	if len(prefix) != s.PrefixLen {
		return fmt.Errorf("%w: %s requires %d bytes, got %d",
			ErrBadPrefixLen, s.Algorithm, s.PrefixLen, len(prefix))
	}
	return nil
}

// BuildIV assembles the full IV as prefix || partial WITHOUT any padding.
// It fails if either component has an unexpected length. Because no padding is
// performed, a short partial IV can never be promoted into a valid IV that
// collides with another message.
func (s IVSpec) BuildIV(prefix, partial []byte) ([]byte, error) {
	if err := s.validatePrefix(prefix); err != nil {
		return nil, err
	}
	if err := s.ValidatePartialIV(partial); err != nil {
		return nil, err
	}

	iv := make([]byte, 0, s.IVLen())
	iv = append(iv, prefix...)
	iv = append(iv, partial...)
	return iv, nil
}

// readFull fills buf using rng, returning an error if fewer than len(buf)
// bytes were obtained.
func readFull(rng io.Reader, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := rng.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
		if n == 0 {
			return total, io.ErrNoProgress
		}
	}
	return total, nil
}

// GeneratePartialIV draws a fresh partial IV of exactly the required length
// from the provided random source (normally crypto/rand.Reader). Length is
// enforced by construction, so the result always satisfies ValidatePartialIV
// and may participate in IV / randomness construction.
func (s IVSpec) GeneratePartialIV(rng io.Reader) ([]byte, error) {
	if rng == nil {
		rng = rand.Reader
	}
	partial := make([]byte, s.PartialIVLen)
	if _, err := readFull(rng, partial); err != nil {
		return nil, fmt.Errorf("netsec: failed to generate partial IV: %w", err)
	}
	return partial, nil
}

// GenerateIV builds a full IV using a freshly generated, correctly sized
// partial IV appended to prefix. It applies the same strict, no-padding rules
// as BuildIV.
func (s IVSpec) GenerateIV(rng io.Reader, prefix []byte) ([]byte, error) {
	if err := s.validatePrefix(prefix); err != nil {
		return nil, err
	}
	partial, err := s.GeneratePartialIV(rng)
	if err != nil {
		return nil, err
	}
	return s.BuildIV(prefix, partial)
}
