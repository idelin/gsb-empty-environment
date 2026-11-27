package h2guard

import (
	"encoding/binary"
)

func (p *Parser) finishBlock() (*Event, error) {
	block := make([]byte, 0, p.block.bytes)
	for _, frag := range p.block.fragments {
		block = append(block, frag...)
	}
	streamID := p.block.streamID
	frameType := p.block.frameType
	p.block = headerBlockState{}

	fields, err := p.hpack.Decode(block)
	if err != nil {
		if ce, ok := AsConnError(err); ok {
			return nil, p.fatal(ce)
		}
		return nil, p.fatal(connError(ErrCodeCompression, "hpack decode failed: %v", err))
	}
	headers, err := validateHeaders(fields)
	if err != nil {
		return nil, p.fatal(err.(*ConnError))
	}

	p.lastProcessedID = maxU32(p.lastProcessedID, streamID)

	if frameType == FramePushPromise {
		return &Event{
			Type:     EventPushPromise,
			StreamID: streamID,
			Headers:  headers,
			Fields:   fields,
		}, nil
	}

	st := p.streams[streamID]
	if st == nil {
		st = &streamState{}
		p.streams[streamID] = st
	}
	st.state = streamOpen
	st.hasCL = headers.HasContentLength
	st.contentLen = headers.ContentLength

	return &Event{
		Type:     EventHeaders,
		StreamID: streamID,
		Headers:  headers,
		Fields:   fields,
	}, nil
}

func (p *Parser) handleHeaders(f *Frame) (*Event, error) {
	if f.StreamID == 0 {
		return nil, p.fatal(connError(ErrCodeProtocol, "HEADERS on stream 0"))
	}
	if f.StreamID%2 == 0 {
		return nil, p.fatal(connError(ErrCodeProtocol, "client HEADERS on even stream id %d", f.StreamID))
	}
	if f.StreamID <= p.lastClientStreamID {
		return nil, p.fatal(connError(ErrCodeProtocol,
			"stream id %d is not strictly increasing (last=%d)", f.StreamID, p.lastClientStreamID))
	}
	if st := p.streams[f.StreamID]; st != nil && st.state != streamIdle {
		return nil, p.fatal(connError(ErrCodeProtocol, "HEADERS reopens stream %d", f.StreamID))
	}

	blockPayload, perr := stripHeadersPadding(f)
	if perr != nil {
		return nil, p.fatal(perr)
	}

	// Record the opening only after the frame is structurally valid.
	p.lastClientStreamID = f.StreamID
	if p.rr.RecordOpen(p.now()) {
		return nil, p.tripRapidReset()
	}

	ev, err := p.startBlock(f, FrameHeaders, blockPayload, 0)
	if err != nil || ev == nil {
		return ev, err
	}
	ev.EndStream = f.EndStream()
	p.applyEndStream(f.StreamID, f.EndStream())
	return ev, nil
}

func (p *Parser) handlePushPromise(f *Frame) (*Event, error) {
	if !p.cfg.AllowClientPushPromise {
		return nil, p.fatal(connError(ErrCodeProtocol, "client must not send PUSH_PROMISE"))
	}
	if f.StreamID == 0 {
		return nil, p.fatal(connError(ErrCodeProtocol, "PUSH_PROMISE on stream 0"))
	}
	blockPayload, _, perr := stripPushPromise(f)
	if perr != nil {
		return nil, p.fatal(perr)
	}
	return p.startBlock(f, FramePushPromise, blockPayload, 0)
}

func stripHeadersPadding(f *Frame) ([]byte, *ConnError) {
	payload := f.Payload
	if f.Priority() {
		if len(payload) < 5 {
			return nil, connError(ErrCodeFrameSize, "HEADERS priority section truncated")
		}
		payload = payload[5:]
	}
	if f.Padded() {
		if len(payload) < 1 {
			return nil, connError(ErrCodeFrameSize, "HEADERS pad length missing")
		}
		padLen := int(payload[0])
		payload = payload[1:]
		if padLen > len(payload) {
			return nil, connError(ErrCodeProtocol, "HEADERS padding longer than payload")
		}
		payload = payload[:len(payload)-padLen]
	}
	return payload, nil
}

func stripPushPromise(f *Frame) ([]byte, uint32, *ConnError) {
	payload := f.Payload
	if f.Padded() {
		if len(payload) < 1 {
			return nil, 0, connError(ErrCodeFrameSize, "PUSH_PROMISE pad length missing")
		}
		padLen := int(payload[0])
		payload = payload[1:]
		if padLen > len(payload) {
			return nil, 0, connError(ErrCodeProtocol, "PUSH_PROMISE padding longer than payload")
		}
		payload = payload[:len(payload)-padLen]
	}
	if len(payload) < 4 {
		return nil, 0, connError(ErrCodeFrameSize, "PUSH_PROMISE promised stream id missing")
	}
	promisedID := binary.BigEndian.Uint32(payload[:4]) & MaxStreamID
	return payload[4:], promisedID, nil
}

func (p *Parser) applyEndStream(streamID uint32, end bool) {
	if !end {
		return
	}
	st := p.streams[streamID]
	if st == nil {
		return
	}
	st.ended = true
	st.state = streamHCLocal
}
