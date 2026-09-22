package correlator

import (
	"encoding/binary"
	"errors"
)

var (
	// ErrIncomplete means the buffer does not yet hold a full frame.
	// It is recoverable: feed more bytes and try again.
	ErrIncomplete = errors.New("correlator: incomplete frame, need more data")

	// ErrInvalid means the bytes are permanently malformed. It is
	// terminal for the stream: no amount of additional data will fix it.
	ErrInvalid = errors.New("correlator: invalid frame")
)

// Parser incrementally decodes frames from an arbitrarily chunked byte
// stream. Memory use is bounded by maxBuffered, and a frame's declared
// payload length is validated against maxPayload before any payload
// allocation happens.
type Parser struct {
	buf         []byte
	maxPayload  int
	maxBuffered int
}

// NewParser returns a Parser. maxPayload is the per-frame payload quota;
// maxBuffered bounds bytes held while waiting for a frame to complete.
// Non-positive or absurd values fall back to safe defaults.
func NewParser(maxPayload, maxBuffered int) *Parser {
	if maxPayload <= 0 || maxPayload > maxPayloadCap {
		maxPayload = maxPayloadCap
	}
	if min := headerSize + maxPayload; maxBuffered < min {
		maxBuffered = min
	}
	return &Parser{maxPayload: maxPayload, maxBuffered: maxBuffered}
}

// Feed appends one chunk of the stream. Chunks may split frames at any
// byte boundary. Feed reports ErrInvalid if the peer is buffering more
// than maxBuffered without completing a frame.
func (p *Parser) Feed(chunk []byte) error {
	if len(p.buf)+len(chunk) > p.maxBuffered {
		return ErrInvalid
	}
	p.buf = append(p.buf, chunk...)
	return nil
}

// Next decodes one frame from the buffer.
//
//	ErrIncomplete: not enough bytes yet; keep feeding.
//	ErrInvalid:    corrupt stream (bad magic, unknown kind, or a declared
//	               payload length over quota — checked before allocating).
func (p *Parser) Next() (Message, error) {
	if len(p.buf) < headerSize {
		return Message{}, ErrIncomplete
	}
	if p.buf[0] != magic0 || p.buf[1] != magic1 {
		return Message{}, ErrInvalid
	}
	kind := Kind(p.buf[2])
	if kind != KindRequest && kind != KindResponse {
		return Message{}, ErrInvalid
	}
	epoch := binary.BigEndian.Uint64(p.buf[3:11])
	seq := binary.BigEndian.Uint64(p.buf[11:19])
	declared := binary.BigEndian.Uint32(p.buf[19:23])
	if declared > uint32(p.maxPayload) {
		// Quota check happens here, before any payload allocation.
		return Message{}, ErrInvalid
	}
	need := headerSize + int(declared)
	if len(p.buf) < need {
		return Message{}, ErrIncomplete
	}
	payload := make([]byte, declared)
	copy(payload, p.buf[headerSize:need])
	p.buf = p.buf[need:]
	if len(p.buf) == 0 {
		p.buf = nil // release the backing array once fully consumed
	}
	return Message{Kind: kind, Epoch: epoch, Seq: seq, Payload: payload}, nil
}
