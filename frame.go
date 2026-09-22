package fsm

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Wire format: [2-byte big-endian length][1-byte type][body...].
// The length covers the type byte plus the body. DATA frames carry a
// 4-byte big-endian sequence number at the start of the body.
const (
	HeaderSize = 2
	TypeSize   = 1
	SeqSize    = 4

	// DefaultMaxFrame is the default length quota for one frame.
	DefaultMaxFrame = 1 << 16
)

// Message type bytes.
const (
	TypeHello byte = 0x01
	TypePing  byte = 0x02
	TypeData  byte = 0x03
	TypeBye   byte = 0x04
)

// ErrNeedMoreData means the buffer holds an incomplete frame; feed more
// bytes and try again. It is distinct from corruption.
var ErrNeedMoreData = errors.New("frame: need more data")

// BadFrameError means the bytes are corrupt; the stream must be dropped.
type BadFrameError struct {
	Reason string
}

func (e *BadFrameError) Error() string {
	return "frame: bad frame: " + e.Reason
}

// IsBadFrame reports whether err is a *BadFrameError.
func IsBadFrame(err error) bool {
	_, ok := err.(*BadFrameError)
	return ok
}

// Frame is one decoded message.
type Frame struct {
	Type    byte
	Seq     uint32 // valid for TypeData
	Payload []byte
}

// Encode serializes one frame.
func Encode(f Frame) []byte {
	body := TypeSize + len(f.Payload)
	if f.Type == TypeData {
		body += SeqSize
	}
	out := make([]byte, HeaderSize+body)
	binary.BigEndian.PutUint16(out, uint16(body))
	out[HeaderSize] = f.Type
	if f.Type == TypeData {
		binary.BigEndian.PutUint32(out[HeaderSize+TypeSize:], f.Seq)
		copy(out[HeaderSize+TypeSize+SeqSize:], f.Payload)
	} else {
		copy(out[HeaderSize+TypeSize:], f.Payload)
	}
	return out
}

// Parser incrementally decodes frames from arbitrarily chunked bytes.
type Parser struct {
	buf []byte
	max int // length quota, enforced before any allocation
}

// NewParser returns a Parser rejecting frames larger than maxFrame bytes.
// A non-positive maxFrame selects DefaultMaxFrame.
func NewParser(maxFrame int) *Parser {
	if maxFrame <= 0 {
		maxFrame = DefaultMaxFrame
	}
	return &Parser{max: maxFrame}
}

// Feed appends one arbitrary chunk of input.
func (p *Parser) Feed(b []byte) { p.buf = append(p.buf, b...) }

// Buffered reports how many undecoded bytes are held.
func (p *Parser) Buffered() int { return len(p.buf) }

// Next pops one frame from the buffer.
//
//   - ErrNeedMoreData: the frame is incomplete (insufficient data); the
//     buffered bytes are kept and the caller should feed more.
//   - *BadFrameError: the bytes are corrupt (bad data); the stream is
//     unrecoverable and must be dropped.
//
// The declared length is checked against the quota BEFORE any body bytes
// are sliced or copied, so an oversized length never triggers allocation.
func (p *Parser) Next() (*Frame, error) {
	if len(p.buf) < HeaderSize {
		return nil, ErrNeedMoreData
	}
	n := int(binary.BigEndian.Uint16(p.buf))
	if n < TypeSize {
		return nil, &BadFrameError{Reason: fmt.Sprintf("declared length %d smaller than type byte", n)}
	}
	if n > p.max {
		return nil, &BadFrameError{Reason: fmt.Sprintf("declared length %d exceeds quota %d", n, p.max)}
	}
	if len(p.buf) < HeaderSize+n {
		return nil, ErrNeedMoreData
	}
	raw := p.buf[HeaderSize : HeaderSize+n]
	f := &Frame{Type: raw[0]}
	body := raw[TypeSize:]
	if f.Type == TypeData {
		if len(body) < SeqSize {
			return nil, &BadFrameError{Reason: "DATA frame shorter than sequence number"}
		}
		f.Seq = binary.BigEndian.Uint32(body)
		body = body[SeqSize:]
	}
	if len(body) > 0 {
		f.Payload = make([]byte, len(body))
		copy(f.Payload, body)
	}
	p.buf = p.buf[HeaderSize+n:]
	return f, nil
}
