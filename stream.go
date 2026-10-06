// Package streamrand provides deterministic random streams derived from a
// master seed and hierarchical task paths.
//
// Design guarantees:
//   - Derivation is a pure function of (parent seed, path): the order in
//     which sibling streams are derived never changes any child's output.
//   - Each stream owns its state; advancing one stream never affects its
//     parent or siblings.
//   - Path segments are length-prefixed before hashing, so segment
//     boundaries are unambiguous: ("ab","c") != ("a","bc").
//   - Derivation composes per segment: Derive(a, b) == Derive(a).Derive(b).
//
// Fixed public integer recurrence (SplitMix64):
//
//	state += 0x9E3779B97F4A7C15
//	z = state
//	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
//	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
//	output = z ^ (z >> 31)
//
// Fixed public derivation function, applied per path segment in order:
//
//	seed = Mix64(seed ^ Mix64(fnv1a64(encode(segment)) ^ 0xD1B54A32D192ED03))
//
// Fixed public segment encoding: uvarint(len(segment)) || segment bytes.
//
// Fixed public state encoding (big-endian):
//
//	magic uint32 = 0x53524E44 ("SRND")
//	version uint32 = StateVersion
//	state uint64
package streamrand

import (
	"encoding/binary"
	"errors"
)

// StateVersion is the current persisted-state format version.
const StateVersion uint32 = 1

const (
	magic uint32 = 0x53524E44 // "SRND"

	gamma uint64 = 0x9E3779B97F4A7C15 // SplitMix64 increment
	mul1  uint64 = 0xBF58476D1CE4E5B9
	mul2  uint64 = 0x94D049BB133111EB

	deriveDomain uint64 = 0xD1B54A32D192ED03 // domain separator for derivation

	fnvOffset uint64 = 14695981039346656037
	fnvPrime  uint64 = 1099511628211
)

// ErrBadState is returned when decoding a malformed or unsupported state.
var ErrBadState = errors.New("streamrand: invalid or unsupported state encoding")

// Stream is an independent deterministic random stream.
type Stream struct {
	state uint64
}

// New returns the root stream for a master seed.
func New(seed uint64) *Stream {
	return &Stream{state: seed}
}

// Mix64 is the fixed public 64-bit mixing function (SplitMix64 finalizer).
func Mix64(z uint64) uint64 {
	z = (z ^ (z >> 30)) * mul1
	z = (z ^ (z >> 27)) * mul2
	return z ^ (z >> 31)
}

// EncodeSegment encodes one path segment unambiguously as
// uvarint(length) followed by the raw bytes.
func EncodeSegment(segment string) []byte {
	buf := make([]byte, 0, binary.MaxVarintLen64+len(segment))
	var tmp [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(tmp[:], uint64(len(segment)))
	buf = append(buf, tmp[:n]...)
	return append(buf, segment...)
}

// EncodePath encodes a whole path as the concatenation of its encoded
// segments. Because every segment is length-prefixed, segment boundaries
// are unambiguous: EncodePath("ab", "c") != EncodePath("a", "bc").
func EncodePath(segments ...string) []byte {
	var buf []byte
	for _, s := range segments {
		buf = append(buf, EncodeSegment(s)...)
	}
	return buf
}

// hashSegment is FNV-1a 64 over the encoded segment.
func hashSegment(segment string) uint64 {
	h := fnvOffset
	for _, b := range EncodeSegment(segment) {
		h ^= uint64(b)
		h *= fnvPrime
	}
	return h
}

// DeriveSeed is the fixed public derivation function. It folds each path
// segment into the seed in order, so DeriveSeed(seed, a, b) equals
// DeriveSeed(DeriveSeed(seed, a), b).
func DeriveSeed(seed uint64, segments ...string) uint64 {
	for _, s := range segments {
		seed = Mix64(seed ^ Mix64(hashSegment(s)^deriveDomain))
	}
	return seed
}

// Derive returns the child stream for the given path segments. The receiver
// is not modified, so derivation order among siblings is irrelevant.
func (s *Stream) Derive(segments ...string) *Stream {
	return &Stream{state: DeriveSeed(s.state, segments...)}
}

// Uint64 advances the stream and returns the next value.
func (s *Stream) Uint64() uint64 {
	s.state += gamma
	return Mix64(s.state)
}

// Int63 returns a non-negative 63-bit integer.
func (s *Stream) Int63() int64 {
	return int64(s.Uint64() >> 1)
}

// Float64 returns a value in [0.0, 1.0).
func (s *Stream) Float64() float64 {
	return float64(s.Uint64()>>11) * (1.0 / (1 << 53))
}

// Read fills p with deterministic bytes. It never returns an error.
func (s *Stream) Read(p []byte) (int, error) {
	for i := 0; i < len(p); {
		x := s.Uint64()
		for j := 0; j < 8 && i < len(p); j++ {
			p[i] = byte(x)
			x >>= 8
			i++
		}
	}
	return len(p), nil
}

// Seed returns the current internal state (position) of the stream.
func (s *Stream) Seed() uint64 {
	return s.state
}

// MarshalBinary encodes the stream state using the fixed public format.
func (s *Stream) MarshalBinary() ([]byte, error) {
	buf := make([]byte, 16)
	binary.BigEndian.PutUint32(buf[0:4], magic)
	binary.BigEndian.PutUint32(buf[4:8], StateVersion)
	binary.BigEndian.PutUint64(buf[8:16], s.state)
	return buf, nil
}

// UnmarshalBinary restores a stream state, rejecting unknown versions.
func (s *Stream) UnmarshalBinary(data []byte) error {
	if len(data) != 16 ||
		binary.BigEndian.Uint32(data[0:4]) != magic ||
		binary.BigEndian.Uint32(data[4:8]) != StateVersion {
		return ErrBadState
	}
	s.state = binary.BigEndian.Uint64(data[8:16])
	return nil
}
