// Package wsprobe implements a passive WebSocket probe: it parses a
// client-to-server byte stream delivered in arbitrarily sized chunks,
// reassembles fragmented messages, validates UTF-8 incrementally on text
// messages, and searches for a forbidden substring across fragment
// boundaries. Interleaved control frames are handled per RFC 6455.
package wsprobe

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"unicode/utf8"
)

// Opcodes defined by RFC 6455.
const (
	OpContinuation = 0x0
	OpText         = 0x1
	OpBinary       = 0x2
	OpClose        = 0x8
	OpPing         = 0x9
	OpPong         = 0xA
)

// Outcome describes how the stream ended once Finish is called.
type Outcome int

const (
	// OutcomeOK means the stream ended exactly on a frame boundary with no
	// unfinished fragmented message.
	OutcomeOK Outcome = iota
	// OutcomeTruncated means the stream ended mid-frame or with an
	// unterminated fragmented message.
	OutcomeTruncated
	// OutcomeProtocolError means the stream violated RFC 6455.
	OutcomeProtocolError
)

func (o Outcome) String() string {
	switch o {
	case OutcomeOK:
		return "ok"
	case OutcomeTruncated:
		return "truncated"
	case OutcomeProtocolError:
		return "protocol-error"
	}
	return "unknown"
}

// ProtocolError describes an RFC 6455 violation detected at a frame.
type ProtocolError struct {
	Frame  int
	Reason string
}

func (e *ProtocolError) Error() string {
	return fmt.Sprintf("frame %d: %s", e.Frame, e.Reason)
}

// Result is returned by Finish.
type Result struct {
	Outcome Outcome
	Err     error // non-nil when Outcome == OutcomeProtocolError
	Detail  string
}

// Frame is a fully parsed and unmasked WebSocket frame.
type Frame struct {
	Index   int
	Fin     bool
	Opcode  byte
	Payload []byte // unmasked
}

// Control reports whether the frame is a control frame.
func (f Frame) Control() bool { return f.Opcode >= 0x8 }

// Hit reports one occurrence of the forbidden substring inside a text
// message. Offsets refer to positions within the unmasked payload of the
// given frame; EndOffset is exclusive.
type Hit struct {
	MessageIndex int
	StartFrame   int
	StartOffset  int
	EndFrame     int
	EndOffset    int
	Text         string
}

func (h Hit) String() string {
	return fmt.Sprintf("msg %d: %q at frame %d offset %d .. frame %d offset %d",
		h.MessageIndex, h.Text, h.StartFrame, h.StartOffset, h.EndFrame, h.EndOffset)
}

// Events holds optional callbacks invoked during parsing.
type Events struct {
	OnFrame func(Frame)
	OnHit   func(Hit)
}

// origin maps one byte of the current search window back to the frame and
// payload offset it came from.
type origin struct {
	frame  int
	offset int
}

// Parser is a streaming WebSocket frame parser. Create it with NewParser, push
// bytes with Feed, and finalize with Finish. It is not safe for concurrent
// use.
type Parser struct {
	needle []byte
	ev     Events

	buf        []byte
	frameIndex int
	msgIndex   int

	inMessage bool
	msgText   bool

	utf8Pending []byte // incomplete trailing rune of the current text message

	tail     []byte   // last len(needle)-1 bytes of the current text message
	tailOrig []origin // per-byte origins of tail

	err error
}

// NewParser creates a Parser searching for needle inside text messages. An
// empty needle disables searching. Client frames are required to be masked.
func NewParser(needle string, ev Events) *Parser {
	return &Parser{needle: []byte(needle), ev: ev}
}

// Feed consumes the next chunk of the stream. Chunks may split the stream at
// any byte boundary. Once a protocol error has been detected, further feeds
// are ignored.
func (p *Parser) Feed(data []byte) {
	if p.err != nil {
		return
	}
	p.buf = append(p.buf, data...)
	for p.err == nil {
		consumed, ok := p.parseFrame()
		if !ok {
			return
		}
		p.buf = p.buf[consumed:]
	}
}

// Finish reports the final outcome of the stream at EOF.
func (p *Parser) Finish() Result {
	if p.err != nil {
		return Result{Outcome: OutcomeProtocolError, Err: p.err}
	}
	if len(p.buf) > 0 {
		return Result{
			Outcome: OutcomeTruncated,
			Detail:  fmt.Sprintf("%d unconsumed byte(s): incomplete frame header or payload", len(p.buf)),
		}
	}
	if p.inMessage {
		return Result{
			Outcome: OutcomeTruncated,
			Detail:  "fragmented message missing its final continuation frame",
		}
	}
	return Result{Outcome: OutcomeOK}
}

func (p *Parser) fail(format string, args ...any) {
	p.err = &ProtocolError{Frame: p.frameIndex, Reason: fmt.Sprintf(format, args...)}
}

// parseFrame attempts to parse one frame from the front of the buffer. It
// returns the number of bytes consumed and whether a full frame was
// available.
func (p *Parser) parseFrame() (int, bool) {
	b := p.buf
	if len(b) < 2 {
		return 0, false
	}
	fin := b[0]&0x80 != 0
	opcode := b[0] & 0x0f
	masked := b[1]&0x80 != 0
	length := uint64(b[1] & 0x7f)

	if b[0]&0x70 != 0 {
		p.fail("reserved bits set without negotiated extension")
		return 0, false
	}

	hdr := 2
	switch length {
	case 126:
		if len(b) < hdr+2 {
			return 0, false
		}
		length = uint64(binary.BigEndian.Uint16(b[hdr:]))
		hdr += 2
	case 127:
		if len(b) < hdr+8 {
			return 0, false
		}
		length = binary.BigEndian.Uint64(b[hdr:])
		if length>>63 != 0 {
			p.fail("64-bit payload length has most significant bit set")
			return 0, false
		}
		hdr += 8
	}

	if !masked {
		p.fail("unmasked frame from client")
		return 0, false
	}
	if len(b) < hdr+4 {
		return 0, false
	}
	maskKey := b[hdr : hdr+4]
	hdr += 4

	if length > uint64(len(b)-hdr) {
		return 0, false // payload not fully arrived yet
	}

	payload := make([]byte, length)
	for i := range payload {
		payload[i] = b[hdr+i] ^ maskKey[i%4]
	}

	p.dispatch(Frame{
		Index:   p.frameIndex,
		Fin:     fin,
		Opcode:  opcode,
		Payload: payload,
	})
	p.frameIndex++
	return hdr + int(length), true
}

func (p *Parser) dispatch(f Frame) {
	if p.ev.OnFrame != nil {
		p.ev.OnFrame(f)
	}
	switch {
	case f.Opcode >= 0x8:
		if f.Opcode > OpPong {
			p.fail("reserved control opcode 0x%x", f.Opcode)
			return
		}
		if !f.Fin {
			p.fail("fragmented control frame")
			return
		}
		if len(f.Payload) > 125 {
			p.fail("control frame payload exceeds 125 bytes")
			return
		}
		if f.Opcode == OpClose && len(f.Payload) == 1 {
			p.fail("close frame with 1-byte payload")
		}
	case f.Opcode == OpContinuation:
		if !p.inMessage {
			p.fail("continuation frame without an open message")
			return
		}
		p.processData(f)
		if p.err == nil && f.Fin {
			p.endMessage()
		}
	case f.Opcode == OpText || f.Opcode == OpBinary:
		if p.inMessage {
			p.fail("new data frame before final continuation of fragmented message")
			return
		}
		p.inMessage = true
		p.msgText = f.Opcode == OpText
		if p.msgText {
			p.utf8Pending = nil
			p.tail = nil
			p.tailOrig = nil
		}
		p.processData(f)
		if p.err == nil && f.Fin {
			p.endMessage()
		}
	default:
		p.fail("reserved data opcode 0x%x", f.Opcode)
	}
}

func (p *Parser) processData(f Frame) {
	if !p.msgText {
		return
	}
	p.validateUTF8(f.Payload)
	if p.err != nil {
		return
	}
	p.search(f)
}

func (p *Parser) endMessage() {
	if p.msgText && len(p.utf8Pending) > 0 {
		p.fail("text message ends with an incomplete UTF-8 sequence")
		return
	}
	p.inMessage = false
	p.msgIndex++
}

// validateUTF8 incrementally validates chunk as the next bytes of the
// current text message. A multi-byte rune may be split across frames.
func (p *Parser) validateUTF8(chunk []byte) {
	data := make([]byte, 0, len(p.utf8Pending)+len(chunk))
	data = append(data, p.utf8Pending...)
	data = append(data, chunk...)
	i := 0
	for i < len(data) {
		if !utf8.FullRune(data[i:]) {
			break
		}
		r, size := utf8.DecodeRune(data[i:])
		if r == utf8.RuneError && size == 1 {
			p.fail("invalid UTF-8 in text message")
			return
		}
		i += size
	}
	p.utf8Pending = append(p.utf8Pending[:0], data[i:]...)
}

// search looks for the needle in the current text message, matching across
// fragment boundaries via a tail window of len(needle)-1 bytes.
func (p *Parser) search(f Frame) {
	if len(p.needle) == 0 {
		return
	}
	combined := make([]byte, 0, len(p.tail)+len(f.Payload))
	combined = append(combined, p.tail...)
	combined = append(combined, f.Payload...)

	origins := make([]origin, 0, len(combined))
	origins = append(origins, p.tailOrig...)
	for i := range f.Payload {
		origins = append(origins, origin{frame: f.Index, offset: i})
	}

	for pos := 0; pos+len(p.needle) <= len(combined); {
		i := bytes.Index(combined[pos:], p.needle)
		if i < 0 {
			break
		}
		start := pos + i
		end := start + len(p.needle)
		if p.ev.OnHit != nil {
			p.ev.OnHit(Hit{
				MessageIndex: p.msgIndex,
				StartFrame:   origins[start].frame,
				StartOffset:  origins[start].offset,
				EndFrame:     origins[end-1].frame,
				EndOffset:    origins[end-1].offset + 1,
				Text:         string(p.needle),
			})
		}
		pos = start + 1 // allow overlapping matches
	}

	keep := len(p.needle) - 1
	if keep > len(combined) {
		keep = len(combined)
	}
	if keep == 0 {
		p.tail = nil
		p.tailOrig = nil
		return
	}
	from := len(combined) - keep
	p.tail = append([]byte(nil), combined[from:]...)
	p.tailOrig = append([]origin(nil), origins[from:]...)
}
