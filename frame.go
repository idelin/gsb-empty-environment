package seqwin

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// ErrNeedMore reports that the current input is a prefix of a valid frame;
// feeding more bytes may complete it. It is distinct from malformed input.
var ErrNeedMore = errors.New("seqwin: need more data")

// FrameTooLargeError reports a declared frame length above the configured
// quota. It is detected from the 2-byte header alone, before any
// payload-sized allocation happens.
type FrameTooLargeError struct{ Len, Max int }

func (e FrameTooLargeError) Error() string {
	return fmt.Sprintf("seqwin: frame length %d exceeds quota %d", e.Len, e.Max)
}

// FrameParser incrementally parses frames of the form
// [uint16 big-endian length][payload] from arbitrarily chunked input.
type FrameParser struct {
	MaxPayload int
	buf        []byte
}

// NewFrameParser creates a parser rejecting frames larger than maxPayload.
func NewFrameParser(maxPayload int) *FrameParser {
	return &FrameParser{MaxPayload: maxPayload}
}

// Write appends a chunk of input. Chunks may split frames anywhere.
func (p *FrameParser) Write(b []byte) { p.buf = append(p.buf, b...) }

// Next returns the next complete frame's payload. It returns ErrNeedMore
// when the buffered bytes are an incomplete frame, and FrameTooLargeError
// when the declared length exceeds the quota (bad data). The length quota
// is checked before any payload-sized allocation.
func (p *FrameParser) Next() ([]byte, error) {
	if len(p.buf) < 2 {
		return nil, ErrNeedMore
	}
	n := int(binary.BigEndian.Uint16(p.buf))
	if n > p.MaxPayload {
		return nil, FrameTooLargeError{Len: n, Max: p.MaxPayload}
	}
	if len(p.buf)-2 < n {
		return nil, ErrNeedMore
	}
	out := make([]byte, n) // allocated only after the quota check
	copy(out, p.buf[2:2+n])
	p.buf = p.buf[2+n:]
	return out, nil
}

// EncodeFrame builds the wire form of one frame.
func EncodeFrame(payload []byte) ([]byte, error) {
	if len(payload) > 0xFFFF {
		return nil, FrameTooLargeError{Len: len(payload), Max: 0xFFFF}
	}
	b := make([]byte, 2+len(payload))
	binary.BigEndian.PutUint16(b, uint16(len(payload)))
	copy(b[2:], payload)
	return b, nil
}
