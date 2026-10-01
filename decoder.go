// Package streamdec implements an incremental, event-driven decoder for a
// length-prefixed object stream. See README.md for the wire format and the
// supported serialization subset.
package streamdec

import "encoding/binary"

// HeaderSize is the fixed size of the big-endian uint32 length header that
// prefixes every message on the wire.
const HeaderSize = 4

// Decoder incrementally decodes a stream of framed objects. It may be fed
// arbitrarily small chunks and can be suspended after any byte; all state
// is kept inside the struct. Events for an object are buffered and only
// published (returned from Feed) once the object is complete.
//
// Decoding is pure: input bytes only drive the state machine and fill
// buffers. No host code is ever executed and no reflection is used.
type Decoder struct {
	maxSize uint32 // maximum accepted message payload size

	header    [HeaderSize]byte
	headerLen int

	msgLen     uint32
	payload    []byte // allocated only after the length header is validated
	payloadLen int
	inPayload  bool

	offset int64 // absolute offset of the next stream byte
	p      parser
}

// NewDecoder returns a Decoder that rejects messages whose payload exceeds
// maxObjectSize bytes.
func NewDecoder(maxObjectSize uint32) *Decoder {
	return &Decoder{maxSize: maxObjectSize}
}

// Offset returns the absolute offset of the next unconsumed stream byte.
func (d *Decoder) Offset() int64 { return d.offset }

// Feed consumes data and returns the buffered events of every object that
// completed while processing this chunk. On error the returned *Error
// carries the absolute stream offset of the offending byte.
func (d *Decoder) Feed(data []byte) ([]Event, error) {
	var out []Event
	for len(data) > 0 {
		if !d.inPayload {
			// The length header itself may arrive split across chunks.
			n := min(HeaderSize-d.headerLen, len(data))
			copy(d.header[d.headerLen:], data[:n])
			d.headerLen += n
			d.offset += int64(n)
			data = data[n:]
			if d.headerLen == HeaderSize {
				if err := d.beginMessage(); err != nil {
					return nil, err
				}
			}
			continue
		}
		n := min(int(d.msgLen)-d.payloadLen, len(data))
		for _, b := range data[:n] {
			abs := d.offset
			d.payload[d.payloadLen] = b
			d.payloadLen++
			d.offset++
			if err := d.p.feed(b, abs); err != nil {
				return nil, err
			}
			if d.p.complete {
				out = append(out, d.p.events...)
				d.p.events = nil
				d.p.complete = false
			}
		}
		data = data[n:]
		if d.payloadLen == int(d.msgLen) {
			if d.p.state != psDone {
				return nil, errf(d.offset, "message of %d bytes ended with unclosed object", d.msgLen)
			}
			d.inPayload = false
			d.headerLen = 0
			d.payload = nil
		}
	}
	return out, nil
}

// Finish reports whether the stream ended on a clean message boundary. It
// returns an error if a length header or an object is only partially
// received (unclosed object).
func (d *Decoder) Finish() error {
	if d.inPayload {
		return errf(d.offset, "finish called with unclosed object: %d of %d payload bytes received",
			d.payloadLen, d.msgLen)
	}
	if d.headerLen > 0 {
		return errf(d.offset-int64(d.headerLen),
			"finish called with truncated length header: %d of %d bytes received",
			d.headerLen, HeaderSize)
	}
	return nil
}

// Cancel aborts the current stream and releases the current object cache
// (payload buffer and buffered events). The decoder is reset and may be
// reused for a new stream.
func (d *Decoder) Cancel() {
	d.headerLen = 0
	d.msgLen = 0
	d.payload = nil
	d.payloadLen = 0
	d.inPayload = false
	d.offset = 0
	d.p.reset()
}

// beginMessage validates the length header before allocating the payload
// buffer, so a hostile or corrupt length can never force a huge allocation.
func (d *Decoder) beginMessage() error {
	d.msgLen = binary.BigEndian.Uint32(d.header[:])
	headerOff := d.offset - HeaderSize
	if d.msgLen == 0 {
		return errf(headerOff, "zero-length message")
	}
	if d.msgLen > d.maxSize {
		return errf(headerOff, "message length %d exceeds maximum %d", d.msgLen, d.maxSize)
	}
	d.payload = make([]byte, d.msgLen)
	d.payloadLen = 0
	d.inPayload = true
	d.p.reset()
	return nil
}
