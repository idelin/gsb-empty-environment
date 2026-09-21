// Package segcrc computes IEEE CRC-32 summaries of byte segments and combines
// segment summaries into the CRC of their concatenation using only the segment
// lengths, without rescanning the underlying data.
package segcrc

import (
	"errors"
	"hash"
	"hash/crc32"
)

// CRC-32/ISO-HDLC (a.k.a. CRC-32/ITU-T, the IEEE checksum used by zip, gzip
// and zlib) parameters.
const (
	// Poly is the reflected generator polynomial 0xEDB88320
	// (reciprocal of 0x04C11DB7).
	Poly = 0xEDB88320
	// Init is the register value the CRC starts from.
	Init = 0xFFFFFFFF
	// XorOut is XORed into the register after every byte has been processed;
	// together with Init this makes the checksum detect leading/trailing zeros.
	XorOut = 0xFFFFFFFF
)

// MaxLen is the largest segment length that can be tracked or combined.
const MaxLen = int64(^uint64(0) >> 1)

var (
	// ErrNegativeLen is returned when a length is negative.
	ErrNegativeLen = errors.New("segcrc: negative length")
	// ErrLenOverflow is returned when bytes exceed MaxLen or an addition
	// of segment lengths overflows int64.
	ErrLenOverflow = errors.New("segcrc: length overflow")
	// ErrBadSummary is returned when an encoded summary is malformed or
	// inconsistent (e.g. length 0 with a non-zero CRC).
	ErrBadSummary = errors.New("segcrc: bad summary encoding")
	// ErrLenMismatch is returned when two summaries that must agree on
	// length do not, including the case of equal CRCs at different lengths.
	ErrLenMismatch = errors.New("segcrc: length mismatch for identical digest")
	// ErrCRCMismatch is returned when a verification digest differs.
	ErrCRCMismatch = errors.New("segcrc: crc mismatch")
	// ErrMissingData means the bytes of a declared segment are unavailable
	// (nothing to read); it is distinct from corrupt data.
	ErrMissingData = errors.New("segcrc: missing data")
	// ErrCorruptData means present bytes failed their declared digest or
	// length check.
	ErrCorruptData = errors.New("segcrc: corrupt data")
)

// Summary is the (digest, byte length) pair of one segment. Length is part of
// the identity: two data streams can share a CRC-32 while having different
// lengths, so CRC alone is never enough to combine or verify segments safely.
type Summary struct {
	CRC    uint32
	Length int64
}

// Empty is the identity summary: the CRC of zero bytes and length zero.
var Empty = Summary{CRC: 0, Length: 0}

// IsEmpty reports whether s is the empty/identity summary.
func (s Summary) IsEmpty() bool { return s == Empty }

// Equal reports whether two summaries agree on both digest and length.
func (s Summary) Equal(o Summary) bool { return s == o }

// Hasher incrementally builds a Summary over an arbitrary sequence of chunks,
// including zero-length chunks.
type Hasher struct {
	h hash.Hash32
	n int64
}

// NewHasher returns a Hasher starting at Init (matching crc32.NewIEEE).
func NewHasher() *Hasher {
	return &Hasher{h: crc32.NewIEEE()}
}

// Write folds a chunk into the running summary. It never fails on I/O but
// returns ErrLenOverflow once the cumulative length would exceed MaxLen.
func (h *Hasher) Write(p []byte) (int, error) {
	if h.n > MaxLen-int64(len(p)) {
		return 0, ErrLenOverflow
	}
	h.h.Write(p) // hash.Hash32.Write never returns an error
	h.n += int64(len(p))
	return len(p), nil
}

// Summary returns the current digest. The hasher keeps its state and can
// accept more data afterwards.
func (h *Hasher) Summary() Summary {
	return Summary{CRC: h.h.Sum32(), Length: h.n}
}

// Reset restores the hasher to its initial state.
func (h *Hasher) Reset() {
	h.h.Reset()
	h.n = 0
}

// Checksum computes the CRC-32 of b in one shot.
func Checksum(b []byte) uint32 { return crc32.ChecksumIEEE(b) }
