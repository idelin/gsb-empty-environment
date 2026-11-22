package lease

import "errors"

// Ring geometry of the 24-bit datagram sequence space.
const (
	SeqBits    = 24
	SeqModulus = uint64(1) << SeqBits
	SeqHalf    = SeqModulus / 2
	SeqMask    = SeqModulus - 1
)

var (
	// ErrAmbiguousSeq is returned when a wire value sits exactly half a
	// ring away from the anchor: forward and backward expansions are
	// equidistant, so the value cannot be uniquely resolved.
	ErrAmbiguousSeq = errors.New("lease: sequence on half-ring boundary cannot be uniquely expanded")
	// ErrSeqOutOfSpan is returned when the nearest expansion of a wire
	// value exceeds the declared maximum span.
	ErrSeqOutOfSpan = errors.New("lease: sequence outside declared span")
	// ErrInvalidSpan is returned when maxSpan is not smaller than half
	// the ring, which would make expansion ambiguous by construction.
	ErrInvalidSpan = errors.New("lease: max span must be smaller than half the ring")
	// ErrInvalidWire is returned when a wire value does not fit in the
	// 24-bit ring.
	ErrInvalidWire = errors.New("lease: wire sequence exceeds 24 bits")
)

// Expand resolves a 24-bit wire sequence number into a full logical
// sequence number relative to anchor. Values up to maxSpan ahead of or
// behind the anchor are accepted. A wire value exactly SeqHalf away from
// the anchor is rejected with ErrAmbiguousSeq because it lies on the
// half-ring boundary and cannot be uniquely expanded.
func Expand(wire uint32, anchor uint64, maxSpan uint32) (uint64, error) {
	if uint64(wire) >= SeqModulus {
		return 0, ErrInvalidWire
	}
	if uint64(maxSpan) >= SeqHalf {
		return 0, ErrInvalidSpan
	}
	anchorWire := anchor & SeqMask
	forward := (uint64(wire) + SeqModulus - anchorWire) & SeqMask
	if forward == SeqHalf {
		return 0, ErrAmbiguousSeq
	}
	if forward < SeqHalf {
		if forward > uint64(maxSpan) {
			return 0, ErrSeqOutOfSpan
		}
		return anchor + forward, nil
	}
	backward := SeqModulus - forward
	if backward > uint64(maxSpan) || backward > anchor {
		return 0, ErrSeqOutOfSpan
	}
	return anchor - backward, nil
}
