// Package proto implements a length-prefixed binary protocol with an
// explicit, compiled state-transition table.
package proto

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Wire format: [2-byte big-endian length][1-byte type][payload...].
// length counts the type byte plus payload, so minimum legal length is 1.
const (
	MaxFrameSize = 1024 // hard quota, checked before any allocation
	headerSize   = 3    // 2 length bytes + 1 type byte
)

// ErrNeedMoreData means the buffer holds a prefix of a valid frame; feed
// more bytes. It is NOT a protocol error.
var ErrNeedMoreData = errors.New("need more data")

// BadDataError means the bytes on the wire are malformed. Unlike
// ErrNeedMoreData, feeding more bytes can never fix this; the caller
// should drop the connection.
type BadDataError struct{ Reason string }

func (e *BadDataError) Error() string { return "bad data: " + e.Reason }

type MsgType byte

const (
	MsgHello    MsgType = 0x01
	MsgHelloAck MsgType = 0x02
	MsgData     MsgType = 0x03 // payload: 4-byte seq + body
	MsgPing     MsgType = 0x04
	MsgPong     MsgType = 0x05
	MsgClose    MsgType = 0x06
)

func (t MsgType) String() string {
	switch t {
	case MsgHello:
		return "Hello"
	case MsgHelloAck:
		return "HelloAck"
	case MsgData:
		return "Data"
	case MsgPing:
		return "Ping"
	case MsgPong:
		return "Pong"
	case MsgClose:
		return "Close"
	default:
		return fmt.Sprintf("Unknown(0x%02x)", byte(t))
	}
}

// Frame is one decoded message. Seq is meaningful only for MsgData.
type Frame struct {
	Type    MsgType
	Seq     uint32
	Payload []byte
}

// Parser incrementally decodes frames from arbitrarily chunked byte input.
type Parser struct {
	buf []byte
}

// Feed appends one arbitrary chunk of input.
func (p *Parser) Feed(b []byte) { p.buf = append(p.buf, b...) }

// Next decodes one frame. It returns ErrNeedMoreData when the buffered
// bytes are a strict prefix of a legal frame, and *BadDataError when the
// bytes are malformed regardless of what arrives later.
func (p *Parser) Next() (Frame, error) {
	if len(p.buf) < headerSize {
		return Frame{}, ErrNeedMoreData
	}
	length := int(binary.BigEndian.Uint16(p.buf[:2]))
	// Quota checks happen BEFORE any allocation: a hostile length field
	// can never force a large or negative-sized allocation.
	if length < 1 {
		return Frame{}, &BadDataError{Reason: "length smaller than type byte"}
	}
	if length > MaxFrameSize {
		return Frame{}, &BadDataError{
			Reason: fmt.Sprintf("declared length %d exceeds quota %d", length, MaxFrameSize),
		}
	}
	if len(p.buf) < 2+length {
		return Frame{}, ErrNeedMoreData
	}
	typ := MsgType(p.buf[2])
	body := make([]byte, length-1) // bounded by MaxFrameSize
	copy(body, p.buf[headerSize:2+length])
	p.buf = p.buf[2+length:]

	f := Frame{Type: typ, Payload: body}
	if typ == MsgData {
		if len(body) < 4 {
			return Frame{}, &BadDataError{Reason: "data frame missing sequence number"}
		}
		f.Seq = binary.BigEndian.Uint32(body[:4])
		f.Payload = body[4:]
	}
	return f, nil
}

// Encode serializes one frame.
func Encode(typ MsgType, seq uint32, payload []byte) ([]byte, error) {
	bodyLen := 1 + len(payload)
	if typ == MsgData {
		bodyLen += 4
	}
	if bodyLen > MaxFrameSize {
		return nil, &BadDataError{Reason: "frame too large to encode"}
	}
	out := make([]byte, 2+bodyLen)
	binary.BigEndian.PutUint16(out, uint16(bodyLen))
	out[2] = byte(typ)
	if typ == MsgData {
		binary.BigEndian.PutUint32(out[3:], seq)
		copy(out[7:], payload)
	} else {
		copy(out[3:], payload)
	}
	return out, nil
}
