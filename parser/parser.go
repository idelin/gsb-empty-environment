// Package parser decodes a length-prefixed binary protocol into share-owned
// frame segments backed by a leased pooled buffer.
package parser

import (
	"encoding/binary"
	"errors"

	"example.org/offlinecli/lease"
)

var (
	// ErrBufferFull means a frame does not fit the leased buffer.
	ErrBufferFull = errors.New("parser: frame exceeds buffer capacity")
	// ErrUnexpectedEOF means the stream ended in the middle of a frame.
	ErrUnexpectedEOF = errors.New("parser: unexpected end of input")
)

const headerSize = 2

// Frame is one decoded message. Payload must not be used after Release.
type Frame struct {
	seg *lease.Segment
}

// Payload returns the frame bytes or an error if the lease is no longer
// readable (release, cancel, timeout or generation reuse).
func (f *Frame) Payload() ([]byte, error) { return f.seg.Bytes() }

// Release returns this frame's shared reference.
func (f *Frame) Release() { f.seg.Release() }

// pendingFrame tracks a frame whose payload spans multiple Read calls.
type pendingFrame struct {
	frame *Frame
	need  int
}

// Parser streams length-prefixed frames into one leased buffer. The framing
// is: 2-byte big-endian length followed by that many payload bytes.
type Parser struct {
	lease  *lease.Lease
	cap    int
	filled int
	// carry holds the bytes of a frame header split across Read calls.
	carry []byte
	// pending is the frame whose payload is still being assembled.
	pending *pendingFrame
}

// New binds a parser to a freshly acquired lease.
func New(l *lease.Lease, capacity int) *Parser {
	return &Parser{lease: l, cap: capacity}
}

// Read feeds more wire bytes and returns every frame it completes. Frames
// whose payload is split across calls keep their segment reference pinned
// until the final bytes arrive. On error, already-decoded frames stay valid
// until the caller releases them; the caller owns the lease lifecycle.
func (p *Parser) Read(chunk []byte) ([]*Frame, error) {
	var frames []*Frame

	for len(chunk) > 0 {
		if p.pending == nil {
			need := headerSize - len(p.carry)
			take := need
			if take > len(chunk) {
				take = len(chunk)
			}
			p.carry = append(p.carry, chunk[:take]...)
			chunk = chunk[take:]
			if len(p.carry) < headerSize {
				break
			}

			frameLen := int(binary.BigEndian.Uint16(p.carry))
			p.carry = p.carry[:0]

			if frameLen == 0 {
				seg, err := p.lease.Segment(p.filled, 0)
				if err != nil {
					return frames, err
				}
				frames = append(frames, &Frame{seg: seg})
				continue
			}
			if p.filled+frameLen > p.cap {
				return frames, ErrBufferFull
			}
			seg, err := p.lease.Segment(p.filled, frameLen)
			if err != nil {
				return frames, err
			}
			p.pending = &pendingFrame{frame: &Frame{seg: seg}, need: frameLen}
		}

		payload, err := p.pending.frame.Payload()
		if err != nil {
			return frames, err
		}
		dst := payload[len(payload)-p.pending.need:]
		n := copy(dst, chunk)
		chunk = chunk[n:]
		p.pending.need -= n
		p.filled += n

		if p.pending.need == 0 {
			frames = append(frames, p.pending.frame)
			p.pending = nil
		}
	}

	return frames, nil
}

// Flush reports an incomplete stream if frame bytes are still missing.
func (p *Parser) Flush() error {
	if p.pending != nil || len(p.carry) > 0 {
		return ErrUnexpectedEOF
	}
	return nil
}
