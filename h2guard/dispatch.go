package h2guard

// Handle processes one already-read frame. On a connection error it returns a
// *ConnError and a GOAWAY frame becomes available via GoAwayFrame; the host
// must stop reading and close the transport. Stream-level *StreamError values
// leave the connection usable; the host should send BuildRSTStream and
// continue. A non-nil Event is returned for frames the host application may
// want to forward, after all security checks have passed.
func (p *Parser) Handle(f *Frame) (*Event, error) {
	if p.closed {
		return nil, p.fatal(connError(ErrCodeInternal, "parser used after GOAWAY"))
	}

	// While a header block is open, only same-stream CONTINUATION is legal.
	if p.block.active {
		return p.handleInBlock(f)
	}

	switch f.Type {
	case FrameHeaders:
		return p.handleHeaders(f)
	case FramePushPromise:
		return p.handlePushPromise(f)
	case FrameContinuation:
		return nil, p.fatal(connError(ErrCodeProtocol, "CONTINUATION without open header block"))
	case FrameData:
		return p.handleData(f)
	case FrameRSTStream:
		return p.handleRST(f)
	case FrameSettings:
		return p.handleSettings(f)
	case FramePing:
		return p.handlePing(f)
	case FrameGoAway:
		return &Event{Type: EventGoAway, StreamID: f.StreamID}, nil
	case FrameWindowUpdate:
		return p.handleWindowUpdate(f)
	case FramePriority:
		return p.handlePriority(f)
	default:
		// Unknown frame types are extension points and must be ignored
		// (RFC 9113 5.1). Framing was validated by ReadFrame.
		return nil, nil
	}
}

func (p *Parser) handleInBlock(f *Frame) (*Event, error) {
	if f.Type != FrameContinuation {
		return nil, p.fatal(connError(ErrCodeProtocol,
			"frame type 0x%x interleaved before END_HEADERS on stream %d", f.Type, p.block.streamID))
	}
	if f.StreamID != p.block.streamID {
		return nil, p.fatal(connError(ErrCodeProtocol,
			"CONTINUATION for stream %d while header block for stream %d is open",
			f.StreamID, p.block.streamID))
	}
	p.block.continuation++
	if p.block.continuation > p.cfg.MaxContinuationFrames {
		return nil, p.fatal(connError(ErrCodeEnhanceYourCalm,
			"CONTINUATION flood: more than %d continuation frames in one header block",
			p.cfg.MaxContinuationFrames))
	}
	if err := p.appendFragment(f.Payload); err != nil {
		return nil, p.fatal(err)
	}
	if !f.EndHeaders() {
		return nil, nil
	}
	return p.finishBlock()
}

func (p *Parser) appendFragment(payload []byte) *ConnError {
	p.block.bytes += len(payload)
	if p.block.bytes > p.cfg.MaxHeaderBlockBytes {
		return connError(ErrCodeEnhanceYourCalm,
			"header block exceeds %d bytes", p.cfg.MaxHeaderBlockBytes)
	}
	cp := make([]byte, len(payload))
	copy(cp, payload)
	p.block.fragments = append(p.block.fragments, cp)
	return nil
}

func (p *Parser) startBlock(f *Frame, typ byte, blockPayload []byte, promiseID uint32) (*Event, error) {
	p.block = headerBlockState{
		active:    true,
		streamID:  f.StreamID,
		frameType: typ,
		promiseID: promiseID,
	}
	if err := p.appendFragment(blockPayload); err != nil {
		return nil, p.fatal(err)
	}
	if f.EndHeaders() {
		return p.finishBlock()
	}
	return nil, nil
}
