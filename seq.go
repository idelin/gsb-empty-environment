package replayhandoff

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Seq is the 40-bit OSCORE sequence number space.
type Seq uint64

// SequenceLimit is the exclusive upper bound of every valid sequence number.
// The largest representable sequence number is SequenceLimit - 1.
const SequenceLimit Seq = Seq(1) << 40

var (
	// ErrMissingEndpoint indicates that an interval omitted one of its ends.
	ErrMissingEndpoint = errors.New("replayhandoff: interval is missing an endpoint")
	// ErrInvertedInterval indicates an interval whose upper bound precedes its
	// lower bound.
	ErrInvertedInterval = errors.New("replayhandoff: interval upper bound must not precede lower bound")
	// ErrOutsideSequenceSpace indicates an endpoint at or above 2^40.
	ErrOutsideSequenceSpace = errors.New("replayhandoff: interval is outside the 40-bit sequence space")
)

// Valid reports whether s is in [0, 2^40).
func (s Seq) Valid() bool {
	return s < SequenceLimit
}

// String returns the decimal representation of s.
func (s Seq) String() string {
	return strconv.FormatUint(uint64(s), 10)
}

func parseSeqEndpoint(text string) (Seq, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, ErrMissingEndpoint
	}
	v, err := strconv.ParseUint(text, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("replayhandoff: invalid sequence endpoint %q: %w", text, err)
	}
	if v > uint64(SequenceLimit) {
		return 0, ErrOutsideSequenceSpace
	}
	return Seq(v), nil
}
