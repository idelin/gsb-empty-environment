package mux

import "encoding/binary"

const defaultMaxPayload = 1 << 14

// Parser incrementally decodes frames from arbitrarily chunked byte input.
// It distinguishes ErrNeedMore (incomplete data, keep reading) from
// *ProtocolError (invalid data, close the connection).
type Parser struct {
	buf        []byte
	maxPayload int
}

func NewParser(maxPayload int) *Parser {
	if maxPayload <= 0 {
		maxPayload = defaultMaxPayload
	}
	return &Parser{maxPayload: maxPayload}
}

// Feed appends raw bytes to the parse buffer.
func (p *Parser) Feed(b []byte) { p.buf = append(p.buf, b...) }

// Buffered reports how many unparsed bytes are currently held.
func (p *Parser) Buffered() int { return len(p.buf) }

// Next returns the next complete frame.
//
//   - ErrNeedMore: the buffer does not yet hold a full frame; feed more bytes.
//   - *ProtocolError: the stream is invalid; the connection must be torn down.
//
// The payload-length quota is enforced as soon as the 9-byte header is
// available, before any payload buffer is allocated, so a hostile length
// field cannot force a large allocation.
func (p *Parser) Next() (*Frame, error) {
	if len(p.buf) < frameHeaderLen {
		return nil, ErrNeedMore
	}
	length := int(p.buf[0])<<16 | int(p.buf[1])<<8 | int(p.buf[2])
	if length > p.maxPayload {
		return nil, &ProtocolError{Code: ErrCodeFrameSize, Msg: "frame payload exceeds limit"}
	}
	typ := FrameType(p.buf[3])
	switch typ {
	case FrameData, FrameWindowUpdate, FrameRstStream:
	default:
		return nil, &ProtocolError{Code: ErrCodeProtocol, Msg: "unknown frame type"}
	}
	if len(p.buf) < frameHeaderLen+length {
		return nil, ErrNeedMore
	}
	sid := binary.BigEndian.Uint32(p.buf[5:9])
	payload := p.buf[frameHeaderLen : frameHeaderLen+length]
	switch typ {
	case FrameData:
		if sid == 0 {
			return nil, &ProtocolError{Code: ErrCodeProtocol, Msg: "DATA frame on stream 0"}
		}
	case FrameWindowUpdate:
		if length != 8 {
			return nil, &ProtocolError{Code: ErrCodeProtocol, Msg: "WINDOW_UPDATE payload must be 8 bytes"}
		}
	case FrameRstStream:
		if sid == 0 || length != 4 {
			return nil, &ProtocolError{Code: ErrCodeProtocol, Msg: "invalid RST_STREAM frame"}
		}
	}
	f := &Frame{Type: typ, Flags: p.buf[4], StreamID: sid}
	if length > 0 {
		f.Payload = make([]byte, length)
		copy(f.Payload, payload)
	}
	p.buf = p.buf[frameHeaderLen+length:]
	if len(p.buf) == 0 {
		p.buf = nil
	}
	return f, nil
}
