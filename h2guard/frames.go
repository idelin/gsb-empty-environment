package h2guard

import (
	"encoding/binary"
	"io"
)

func (p *Parser) handleData(f *Frame) (*Event, error) {
	if f.StreamID == 0 {
		return nil, p.fatal(connError(ErrCodeProtocol, "DATA on stream 0"))
	}
	st := p.streams[f.StreamID]
	if st == nil || st.state == streamIdle {
		return nil, streamError(f.StreamID, ErrCodeStreamClosed, "DATA on unopened stream")
	}
	if st.state == streamClosed || st.ended {
		return nil, streamError(f.StreamID, ErrCodeStreamClosed, "DATA after END_STREAM/RST_STREAM")
	}
	payload := f.Payload
	if f.Padded() {
		if len(payload) < 1 {
			return nil, p.fatal(connError(ErrCodeFrameSize, "DATA pad length missing"))
		}
		padLen := int(payload[0])
		payload = payload[1:]
		if padLen > len(payload) {
			return nil, p.fatal(connError(ErrCodeProtocol, "DATA padding longer than payload"))
		}
		payload = payload[:len(payload)-padLen]
	}

	st.dataBytes += int64(len(payload))
	if st.hasCL && st.dataBytes > st.contentLen {
		return nil, p.fatal(connError(ErrCodeProtocol,
			"DATA payload %d bytes exceeds Content-Length %d on stream %d",
			st.dataBytes, st.contentLen, f.StreamID))
	}

	ev := &Event{
		Type:      EventData,
		StreamID:  f.StreamID,
		Data:      payload,
		EndStream: f.EndStream(),
	}

	if f.EndStream() {
		if st.hasCL && st.dataBytes != st.contentLen {
			return nil, p.fatal(connError(ErrCodeProtocol,
				"Content-Length %d does not match %d DATA bytes on stream %d (smuggling)",
				st.contentLen, st.dataBytes, f.StreamID))
		}
		st.ended = true
		st.state = streamHCLocal
	}
	return ev, nil
}

func (p *Parser) handleRST(f *Frame) (*Event, error) {
	if f.StreamID == 0 {
		return nil, p.fatal(connError(ErrCodeProtocol, "RST_STREAM on stream 0"))
	}
	if len(f.Payload) != 4 {
		return nil, p.fatal(connError(ErrCodeFrameSize, "RST_STREAM payload must be 4 bytes"))
	}
	code := binary.BigEndian.Uint32(f.Payload)
	st := p.streams[f.StreamID]
	if st != nil {
		// A reset before the request/response cycle is done is the Rapid
		// Reset signal. A reset after the client already ended the stream is
		// treated as ordinary teardown and not counted.
		if st.state != streamClosed && !st.ended {
			if p.rr.RecordEarlyReset(p.now()) {
				return nil, p.tripRapidReset()
			}
		}
		st.state = streamClosed
	}
	p.lastProcessedID = maxU32(p.lastProcessedID, f.StreamID)
	return &Event{Type: EventRSTStream, StreamID: f.StreamID, RSTCode: code}, nil
}

func (p *Parser) handleSettings(f *Frame) (*Event, error) {
	if f.StreamID != 0 {
		return nil, p.fatal(connError(ErrCodeProtocol, "SETTINGS must be on stream 0"))
	}
	if f.Ack() {
		if len(f.Payload) != 0 {
			return nil, p.fatal(connError(ErrCodeFrameSize, "SETTINGS ACK must be empty"))
		}
		return &Event{Type: EventSettings, StreamID: 0}, nil
	}
	if len(f.Payload)%6 != 0 {
		return nil, p.fatal(connError(ErrCodeFrameSize, "SETTINGS payload not multiple of 6 bytes"))
	}
	for i := 0; i+6 <= len(f.Payload); i += 6 {
		id := binary.BigEndian.Uint16(f.Payload[i : i+2])
		value := binary.BigEndian.Uint32(f.Payload[i+2 : i+6])
		switch id {
		case 1: // SETTINGS_HEADER_TABLE_SIZE
			p.hpack.maxSize = value
			p.hpack.setMaxSize(value)
		case 5: // SETTINGS_MAX_FRAME_SIZE
			if value < MinMaxFrameSize || value > MaxFrameSizeHard {
				return nil, p.fatal(connError(ErrCodeFrameSize, "SETTINGS_MAX_FRAME_SIZE out of range"))
			}
			p.cfg.MaxFrameSize = value
		}
	}
	return &Event{Type: EventSettings, StreamID: 0}, nil
}

func (p *Parser) handlePing(f *Frame) (*Event, error) {
	if f.StreamID != 0 {
		return nil, p.fatal(connError(ErrCodeProtocol, "PING must be on stream 0"))
	}
	if len(f.Payload) != 8 {
		return nil, p.fatal(connError(ErrCodeFrameSize, "PING payload must be 8 bytes"))
	}
	return &Event{Type: EventPing, StreamID: 0, Data: f.Payload}, nil
}

func (p *Parser) handleWindowUpdate(f *Frame) (*Event, error) {
	if len(f.Payload) != 4 {
		return nil, p.fatal(connError(ErrCodeFrameSize, "WINDOW_UPDATE payload must be 4 bytes"))
	}
	inc := binary.BigEndian.Uint32(f.Payload) & MaxStreamID
	if inc == 0 {
		if f.StreamID == 0 {
			return nil, p.fatal(connError(ErrCodeProtocol, "zero WINDOW_UPDATE increment on connection"))
		}
		return nil, streamError(f.StreamID, ErrCodeProtocol, "zero WINDOW_UPDATE increment")
	}
	return &Event{Type: EventWindowUpdate, StreamID: f.StreamID}, nil
}

func (p *Parser) handlePriority(f *Frame) (*Event, error) {
	if f.StreamID == 0 {
		return nil, p.fatal(connError(ErrCodeProtocol, "PRIORITY on stream 0"))
	}
	if len(f.Payload) != 5 {
		return nil, p.fatal(connError(ErrCodeFrameSize, "PRIORITY payload must be 5 bytes"))
	}
	return &Event{Type: EventPriority, StreamID: f.StreamID}, nil
}

// ReadFrame pulls the next wire frame using the parser's active
// SETTINGS_MAX_FRAME_SIZE.
func (p *Parser) ReadFrame(r io.Reader) (*Frame, error) {
	return ReadFrame(r, p.cfg.MaxFrameSize)
}

// Run consumes frames from r until a connection error or EOF. For each
// validated event it calls onEvent (may be nil). Stream errors are reported
// through onStreamError and do not stop the loop. The returned error is nil on
// clean EOF or the final *ConnError; GoAwayFrame is ready afterwards.
func (p *Parser) Run(r io.Reader, onEvent func(*Event), onStreamError func(*StreamError)) error {
	for {
		f, err := p.ReadFrame(r)
		if err != nil {
			if err == ErrShortRead {
				return nil
			}
			if ce, ok := AsConnError(err); ok {
				return p.fatal(ce)
			}
			return err
		}
		ev, err := p.Handle(f)
		if ev != nil && onEvent != nil {
			onEvent(ev)
		}
		if err != nil {
			if se, ok := AsStreamError(err); ok {
				if onStreamError != nil {
					onStreamError(se)
				}
				continue
			}
			if ce, ok := AsConnError(err); ok {
				return ce
			}
			return err
		}
	}
}
