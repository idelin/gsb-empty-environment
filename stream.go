package bigsqrt

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
)

// ErrDigitLimit is returned by Stream.Feed when a chunk would push the
// accumulated input past the configured digit limit. The offending chunk
// is not stored, and every subsequent Feed fails with the same error:
// the Stream never grows its buffer beyond the limit.
var ErrDigitLimit = errors.New("bigsqrt: decimal digit limit exceeded")

// Stream accumulates non-negative decimal input arriving in chunks.
// It enforces a hard limit on the number of digits it will buffer.
type Stream struct {
	max      int
	buf      []byte
	rejected bool // set once the limit has been exceeded
}

// NewStream returns a Stream accepting at most maxDigits decimal digits.
func NewStream(maxDigits int) *Stream {
	if maxDigits < 1 {
		maxDigits = 1
	}
	return &Stream{max: maxDigits}
}

// Feed appends one chunk of ASCII decimal-digit input. A chunk that would
// exceed the digit limit is rejected in full, the Stream is sealed, and
// ErrDigitLimit is returned; further calls keep failing without
// allocating. Empty chunks are no-ops.
func (s *Stream) Feed(chunk string) error {
	if s.rejected {
		return ErrDigitLimit
	}
	for i := 0; i < len(chunk); i++ {
		if c := chunk[i]; c < '0' || c > '9' {
			return fmt.Errorf("bigsqrt: invalid decimal digit 0x%02x at chunk offset %d", c, i)
		}
	}
	if len(chunk) > s.max-len(s.buf) {
		s.rejected = true
		return ErrDigitLimit
	}
	s.buf = append(s.buf, chunk...)
	return nil
}

// Digits reports how many digits have been accepted so far.
func (s *Stream) Digits() int { return len(s.buf) }

// Rejected reports whether the digit limit has been exceeded.
func (s *Stream) Rejected() bool { return s.rejected }

// Int parses the accumulated digits. Leading zeros are ignored; an
// empty or all-zero stream yields 0. The result is freshly allocated.
func (s *Stream) Int() (*big.Int, error) {
	trimmed := strings.TrimLeft(string(s.buf), "0")
	if trimmed == "" {
		return new(big.Int), nil
	}
	n, ok := new(big.Int).SetString(trimmed, 10)
	if !ok {
		return nil, errors.New("bigsqrt: failed to parse decimal input")
	}
	return n, nil
}
