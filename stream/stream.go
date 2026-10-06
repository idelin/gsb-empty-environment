// Package stream implements deterministic, hierarchically derivable
// pseudo-random streams.
//
// A master seed produces a root Stream. Any stream can derive independent
// child streams by path segment, e.g. root.Child("ingest").Child("shard-7").
// Derivation is a pure function of the parent state and the segment, so:
//
//   - the order in which siblings are derived never changes any child;
//   - advancing one stream never affects its siblings, ancestors, or
//     descendants already derived.
//
// The full algorithm is a fixed, public specification (see README.md):
//
//	Integer recurrence (SplitMix64). State s is a single uint64.
//	Each Uint64 call advances:
//	    s += 0x9E3779B97F4A7C15
//	    z = s
//	    z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
//	    z = (z ^ (z >> 27)) * 0x94D049BB133111EB
//	    z = z ^ (z >> 31)
//	and returns z. All arithmetic is mod 2^64.
//
//	Root state. New(seed) sets s = seed directly.
//
//	Child derivation. The child state is FNV-1a/64 (offset basis
//	14695981039346656037, prime 1099511628211) over the byte sequence:
//	    "gsbstream/child" 0x00 "v1" 0x00   domain separator
//	    parent state, 8 bytes little-endian
//	    uvarint(len(segment))               unambiguous length prefix
//	    segment bytes, verbatim
//	finalized with the SplitMix64 mixing function above applied to the
//	hash value. The parent state is never modified.
//
//	Byte encoding. Read emits successive Uint64 values little-endian,
//	slicing the final word if the buffer is not a multiple of 8.
//
//	Save format (13 bytes): "GSTR" magic, one version byte (0x01),
//	then the 8-byte little-endian state. Restore rejects any other
//	magic, length, or version.
package stream

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	// splitMix64Gamma is the fixed Weyl increment of the recurrence.
	splitMix64Gamma = 0x9E3779B97F4A7C15

	fnvOffsetBasis = 14695981039346656037
	fnvPrime       = 1099511628211

	saveMagic   = "GSTR"
	saveVersion = 1
	saveLen     = 4 + 1 + 8
)

// Stream is an independent deterministic random stream. The zero value is
// not valid; obtain streams from New, Restore, or Child. A Stream is not
// safe for concurrent use.
type Stream struct {
	state uint64
}

// New returns the root stream for a master seed. New(seed) sets the
// internal state to seed verbatim, so New(0).Uint64() is the first
// SplitMix64 output 0xE220A8397B1DCDAF.
func New(seed uint64) *Stream {
	return &Stream{state: seed}
}

// mix64 is the public SplitMix64 finalizer, shared by Uint64 and Child.
func mix64(z uint64) uint64 {
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// Uint64 advances the stream by one step of the fixed recurrence and
// returns the next 64-bit value.
func (s *Stream) Uint64() uint64 {
	s.state += splitMix64Gamma
	return mix64(s.state)
}

// Int63 returns a non-negative 63-bit integer, implementing rand.Source.
func (s *Stream) Int63() int64 {
	return int64(s.Uint64() >> 1)
}

// Float64 returns a value in [0.0, 1.0) with 53 bits of precision.
func (s *Stream) Float64() float64 {
	return float64(s.Uint64()>>11) * (1.0 / (1 << 53))
}

// Read fills p with the fixed byte encoding: successive Uint64 values in
// little-endian order. It never returns an error and always returns len(p).
func (s *Stream) Read(p []byte) (int, error) {
	n := 0
	for n < len(p) {
		word := s.Uint64()
		for i := 0; i < 8 && n < len(p); i++ {
			p[n] = byte(word >> (8 * uint(i)))
			n++
		}
	}
	return n, nil
}

// Child derives the child stream for one path segment. The receiver is not
// modified and not advanced; the result depends only on the receiver's
// current state and seg. Deriving children in any order, before or after
// advancing the parent or any sibling, always yields identical children.
func (s *Stream) Child(seg string) *Stream {
	return &Stream{state: derive(s.state, seg)}
}

// derive is the fixed, public child-derivation function.
func derive(parent uint64, seg string) uint64 {
	h := uint64(fnvOffsetBasis)
	mix := func(b byte) {
		h ^= uint64(b)
		h *= fnvPrime
	}
	for _, b := range []byte("gsbstream/child\x00v1\x00") {
		mix(b)
	}
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], parent)
	for _, b := range buf {
		mix(b)
	}
	n := uint64(len(seg))
	for n >= 0x80 {
		mix(byte(n) | 0x80)
		n >>= 7
	}
	mix(byte(n))
	for i := 0; i < len(seg); i++ {
		mix(seg[i])
	}
	return mix64(h)
}

// Save encodes the stream state in the fixed 13-byte versioned format.
// The stream can later be resumed bit-for-bit with Restore.
func (s *Stream) Save() []byte {
	out := make([]byte, saveLen)
	copy(out, saveMagic)
	out[4] = saveVersion
	binary.LittleEndian.PutUint64(out[5:], s.state)
	return out
}

// Restore rebuilds a stream from Save output. It returns an error if the
// data has the wrong length, magic, or an unsupported version.
func Restore(data []byte) (*Stream, error) {
	if len(data) != saveLen {
		return nil, fmt.Errorf("stream: bad save length %d, want %d", len(data), saveLen)
	}
	if string(data[:4]) != saveMagic {
		return nil, errors.New("stream: bad save magic")
	}
	if data[4] != saveVersion {
		return nil, fmt.Errorf("stream: unsupported save version %d", data[4])
	}
	return &Stream{state: binary.LittleEndian.Uint64(data[5:])}, nil
}
