package netsec

import (
	"errors"
	"fmt"
	"io"
)

// parser states.
const (
	stStart = iota // outside a frame, only VT is legal
	stBody         // inside a frame collecting payload bytes
	stEnd          // FS just seen, expecting CR
	stSkip         // oversized frame: discard until the next VT
)

// Parser is an incremental, stateful MLLP frame parser. Feed accepts byte
// chunks of any size and any alignment; frames and framing errors are queued
// in order and drained with Next. The same event stream is produced
// regardless of where chunk boundaries fall, including a boundary between the
// FS and CR of the trailer.
//
// A Parser is not safe for concurrent use.
type Parser struct {
	state   int
	body    []byte
	events  []event
	hardErr error
	offset  int64 // total bytes consumed, for error context
	maxSize int   // maximum payload length; <= 0 means unlimited
}

type event struct {
	frame []byte // non-nil for a frame event; nil for an error event
	err   error
}

// NewParser returns a Parser that rejects frames whose payload is longer than
// maxFramePayload. A maxFramePayload <= 0 disables the limit.
func NewParser(maxFramePayload int) *Parser {
	return &Parser{maxSize: maxFramePayload}
}

// Feed pushes one chunk of bytes through the state machine. It always consumes
// the whole chunk. Once a fatal protocol error occurs the parser is dead:
// subsequent calls keep returning the same error until Reset is called.
// Frame-level errors (bad trailer, oversized frame) are non-fatal: the
// affected frame is discarded and parsing resynchronizes automatically.
func (p *Parser) Feed(chunk []byte) error {
	if p.hardErr != nil {
		return p.hardErr
	}
	for _, b := range chunk {
		p.offset++
		switch p.state {
		case stStart:
			switch b {
			case VT:
				p.state = stBody
			default:
				return p.fail(fmt.Errorf("%w: byte 0x%02X outside frame at offset %d",
					ErrProtocol, b, p.offset))
			}

		case stBody:
			switch b {
			case VT:
				// A second VT inside an open frame means the stream can no
				// longer be trusted (potential message smuggling).
				return p.fail(fmt.Errorf("%w: second VT(0x0B) inside frame at offset %d",
					ErrProtocol, p.offset))
			case FS:
				p.state = stEnd
			default:
				if p.maxSize > 0 && len(p.body) >= p.maxSize {
					// Payload already filled the limit and another payload
					// byte arrives: frame is too large. Drop everything and
					// look for the next legal start.
					p.body = p.body[:0]
					p.state = stSkip
					p.pushErr(fmt.Errorf("%w: limit %d bytes at offset %d",
						ErrFrameTooLarge, p.maxSize, p.offset))
				} else {
					p.body = append(p.body, b)
				}
			}

		case stEnd:
			switch b {
			case CR:
				p.pushFrame(p.body)
				p.body = p.body[:0]
				p.state = stStart
			case VT:
				// FS followed by something other than CR: the frame is void.
				// The VT starts a fresh frame.
				p.body = p.body[:0]
				p.pushErr(fmt.Errorf("%w at offset %d; resynchronized at VT",
					ErrBadTrailer, p.offset-1))
				p.state = stBody
			default:
				p.body = p.body[:0]
				p.pushErr(fmt.Errorf("%w: got 0x%02X at offset %d; waiting for next VT",
					ErrBadTrailer, b, p.offset))
				p.state = stStart
			}

		case stSkip:
			if b == VT {
				p.state = stBody
			}
		}
	}
	return nil
}

// Next returns the next parsed frame payload, or the next framing error.
// A successful frame is never nil, even when its payload is empty. It returns
// (nil, nil) when no event is currently buffered; feed more bytes or call
// End.
func (p *Parser) Next() (frame []byte, err error) {
	ev, ok := p.nextEvent()
	if !ok {
		return nil, nil
	}
	return ev.frame, ev.err
}

// nextEvent pops the head event, surfacing a sticky hard error once the queue
// has been drained. The ok result is false when no event is available.
func (p *Parser) nextEvent() (ev event, ok bool) {
	if len(p.events) > 0 {
		ev = p.events[0]
		p.events[0] = event{} // drop references so popped frames can be GC'd
		p.events = p.events[1:]
		return ev, true
	}
	if p.hardErr != nil {
		return event{err: p.hardErr}, true
	}
	return event{}, false
}

// End reports an incomplete frame when the byte stream ends while a frame is
// open or after an FS. It returns io.EOF on a clean boundary.
func (p *Parser) End() error {
	if p.hardErr != nil {
		return p.hardErr
	}
	switch p.state {
	case stBody, stEnd, stSkip:
		p.body = p.body[:0]
		p.state = stStart
		return io.ErrUnexpectedEOF
	default:
		return io.EOF
	}
}

// Reset returns the parser to its initial state, clearing queued events and
// any sticky protocol error.
func (p *Parser) Reset() {
	p.state = stStart
	p.body = p.body[:0]
	p.events = nil
	p.hardErr = nil
	p.offset = 0
}

// HasEvents reports whether Next would return an event without more input.
func (p *Parser) HasEvents() bool {
	return len(p.events) > 0 || p.hardErr != nil
}

func (p *Parser) pushFrame(body []byte) {
	frame := make([]byte, len(body))
	copy(frame, body)
	p.events = append(p.events, event{frame: frame})
}

func (p *Parser) pushErr(err error) {
	p.events = append(p.events, event{err: err})
}

func (p *Parser) fail(err error) error {
	p.body = p.body[:0]
	p.state = stStart
	p.hardErr = err
	return err
}

// DefaultReadChunkSize is the buffer size used by FrameReader when the
// caller does not specify one.
const DefaultReadChunkSize = 4096

// FrameReader streams whole MLLP frames from an io.Reader using a Parser. It
// handles arbitrary TCP segmentation transparently.
type FrameReader struct {
	r       io.Reader
	parser  *Parser
	buf     []byte
	readErr error
}

// NewFrameReader wraps r with the per-frame payload limit maxFramePayload.
// A limit <= 0 means unlimited.
func NewFrameReader(r io.Reader, maxFramePayload int) *FrameReader {
	return NewFrameReaderWithChunkSize(r, maxFramePayload, DefaultReadChunkSize)
}

// NewFrameReaderWithChunkSize is NewFrameReader with an explicit read buffer
// size.
func NewFrameReaderWithChunkSize(r io.Reader, maxFramePayload, chunkSize int) *FrameReader {
	if chunkSize <= 0 {
		chunkSize = DefaultReadChunkSize
	}
	return &FrameReader{
		r:      r,
		parser: NewParser(maxFramePayload),
		buf:    make([]byte, chunkSize),
	}
}

// ReadFrame returns the next complete frame payload (a copy that stays valid
// after subsequent reads). Non-fatal frame errors are returned and reading
// continues; a wrapped ErrProtocol is fatal and the connection should be
// closed. io.ErrUnexpectedEOF means the peer stopped mid-frame.
func (fr *FrameReader) ReadFrame() ([]byte, error) {
	for {
		if ev, ok := fr.parser.nextEvent(); ok {
			if ev.err != nil {
				return nil, ev.err
			}
			return ev.frame, nil
		}
		if fr.readErr != nil {
			if endErr := fr.parser.End(); errors.Is(endErr, io.ErrUnexpectedEOF) {
				return nil, endErr
			}
			return nil, fr.readErr
		}
		n, err := fr.r.Read(fr.buf)
		if n > 0 {
			if feedErr := fr.parser.Feed(fr.buf[:n]); feedErr != nil {
				// Fatal protocol error: drain it via the event path below.
				fr.readErr = feedErr
			}
		}
		if err != nil {
			fr.readErr = err
		}
	}
}

// Parser exposes the underlying incremental parser, mainly for tests and for
// callers driving non-blocking I/O themselves.
func (fr *FrameReader) Parser() *Parser { return fr.parser }
