// Package mux implements a multiplexed byte channel: many logical streams
// share one ordered byte stream, with per-stream flow-control windows,
// half-close semantics, and control frames that are prioritized but still
// bounded by a connection-level byte quota.
package mux

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// FrameType identifies the kind of a frame on the wire.
type FrameType byte

const (
	FrameOpen FrameType = iota + 1
	FrameData
	FrameWindowUpdate
	FrameClose
	FrameReset
)

// HeaderSize is type(1) + streamID(4) + length(4).
const HeaderSize = 9

// ErrNeedMoreData means the parser buffer holds a prefix of a valid frame.
// It is distinct from malformed input, which yields *ProtocolError.
var ErrNeedMoreData = errors.New("mux: need more data")

// ProtocolError reports malformed input or a protocol-state violation.
type ProtocolError struct{ Reason string }

func (e *ProtocolError) Error() string { return "mux: protocol error: " + e.Reason }

// Frame is one decoded protocol frame.
type Frame struct {
	Type     FrameType
	StreamID uint32
	Payload  []byte
}

func validType(t FrameType) bool {
	switch t {
	case FrameOpen, FrameData, FrameWindowUpdate, FrameClose, FrameReset:
		return true
	}
	return false
}

// EncodeFrame serializes f, rejecting payloads above maxPayload.
func EncodeFrame(f Frame, maxPayload uint32) ([]byte, error) {
	if !validType(f.Type) {
		return nil, &ProtocolError{Reason: fmt.Sprintf("unknown frame type %d", f.Type)}
	}
	if uint32(len(f.Payload)) > maxPayload {
		return nil, &ProtocolError{Reason: fmt.Sprintf("payload %d exceeds max %d", len(f.Payload), maxPayload)}
	}
	buf := make([]byte, HeaderSize+len(f.Payload))
	buf[0] = byte(f.Type)
	binary.BigEndian.PutUint32(buf[1:5], f.StreamID)
	binary.BigEndian.PutUint32(buf[5:9], uint32(len(f.Payload)))
	copy(buf[HeaderSize:], f.Payload)
	return buf, nil
}

// Parser incrementally decodes frames from arbitrarily chunked byte input.
type Parser struct {
	buf        []byte
	maxPayload uint32
}

func NewParser(maxPayload uint32) *Parser {
	return &Parser{maxPayload: maxPayload}
}

// Feed appends raw bytes to the parse buffer.
func (p *Parser) Feed(b []byte) { p.buf = append(p.buf, b...) }

// Next returns the next complete frame. It returns ErrNeedMoreData when the
// buffer holds only a prefix of a frame, and *ProtocolError when the bytes
// are malformed. The declared length is validated against maxPayload before
// any payload-sized allocation happens.
func (p *Parser) Next() (Frame, error) {
	if len(p.buf) < HeaderSize {
		return Frame{}, ErrNeedMoreData
	}
	ft := FrameType(p.buf[0])
	if !validType(ft) {
		return Frame{}, &ProtocolError{Reason: fmt.Sprintf("unknown frame type %d", ft)}
	}
	length := binary.BigEndian.Uint32(p.buf[5:9])
	if length > p.maxPayload {
		return Frame{}, &ProtocolError{Reason: fmt.Sprintf("declared length %d exceeds max %d", length, p.maxPayload)}
	}
	if uint64(len(p.buf)) < uint64(HeaderSize)+uint64(length) {
		return Frame{}, ErrNeedMoreData
	}
	streamID := binary.BigEndian.Uint32(p.buf[1:5])
	payload := make([]byte, length)
	copy(payload, p.buf[HeaderSize:HeaderSize+length])
	p.buf = p.buf[HeaderSize+length:]
	return Frame{Type: ft, StreamID: streamID, Payload: payload}, nil
}
