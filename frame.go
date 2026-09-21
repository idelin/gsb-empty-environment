package seqwin

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Frame wire layout (all integers big-endian):
//
//	magic   2 bytes  0x5A 0xA5
//	typ     1 byte
//	flags   1 byte
//	length  2 bytes  payload length in bytes
//	payload length bytes
//	crc16   2 bytes  CRC-16/CCITT-FALSE over typ|flags|length|payload
const (
	HeaderLen = 6
	CrcLen    = 2
	maxOnWire = HeaderLen + 0xFFFF + CrcLen
)

var magic = [2]byte{0x5A, 0xA5}

// ErrIncomplete reports that the buffered bytes are a prefix of a valid
// frame so far; more input may complete it. It is never wrapped in a
// CorruptError, so callers can reliably tell "need more data" apart from
// "bad data" with errors.Is(err, ErrIncomplete).
var ErrIncomplete = errors.New("seqwin: incomplete frame, need more data")

// CorruptError reports bytes that can never form a valid frame.
type CorruptError struct{ Reason string }

func (e *CorruptError) Error() string { return "seqwin: corrupt frame: " + e.Reason }

// IsCorrupt reports whether err is a corruption (bad data) error.
func IsCorrupt(err error) bool {
	var ce *CorruptError
	return errors.As(err, &ce)
}

// Frame is one decoded message.
type Frame struct {
	Type    byte
	Flags   byte
	Payload []byte
}

// Parser incrementally decodes frames from arbitrarily chunked input.
// MaxPayload is a quota checked against the declared length before any
// payload-sized allocation happens.
type Parser struct {
	MaxPayload int
	buf        []byte
}

// NewParser returns a parser rejecting payloads larger than maxPayload.
func NewParser(maxPayload int) *Parser { return &Parser{MaxPayload: maxPayload} }

// Feed appends chunk to the internal buffer and returns every frame that
// became complete. It returns nil, nil when more data is needed, and a
// *CorruptError (with the buffer reset) on bad data.
func (p *Parser) Feed(chunk []byte) ([]Frame, error) {
	p.buf = append(p.buf, chunk...)
	var out []Frame
	for len(p.buf) > 0 {
		f, n, err := p.parseOne(p.buf)
		if errors.Is(err, ErrIncomplete) {
			return out, nil
		}
		if err != nil {
			p.buf = p.buf[:0] // stream desynchronized; drop and report
			return out, err
		}
		out = append(out, f)
		p.buf = p.buf[n:]
	}
	return out, nil
}

// parseOne decodes one frame from buf, returning the frame and the bytes
// consumed. ErrIncomplete means the prefix is still plausible.
func (p *Parser) parseOne(buf []byte) (Frame, int, error) {
	if len(buf) < 2 {
		return Frame{}, 0, ErrIncomplete
	}
	if buf[0] != magic[0] || buf[1] != magic[1] {
		return Frame{}, 0, &CorruptError{Reason: "bad magic bytes"}
	}
	if len(buf) < HeaderLen {
		return Frame{}, 0, ErrIncomplete
	}
	declared := int(binary.BigEndian.Uint16(buf[4:6]))
	// Quota check happens before any allocation proportional to declared.
	if declared > p.MaxPayload {
		return Frame{}, 0, &CorruptError{
			Reason: fmt.Sprintf("declared payload %d exceeds quota %d", declared, p.MaxPayload),
		}
	}
	total := HeaderLen + declared + CrcLen
	if len(buf) < total {
		return Frame{}, 0, ErrIncomplete
	}
	body := buf[2 : HeaderLen+declared]
	if got, want := binary.BigEndian.Uint16(buf[HeaderLen+declared:total]), crc16(body); got != want {
		return Frame{}, 0, &CorruptError{
			Reason: fmt.Sprintf("crc mismatch: got 0x%04X want 0x%04X", got, want),
		}
	}
	payload := make([]byte, declared) // declared <= MaxPayload, checked above
	copy(payload, buf[HeaderLen:HeaderLen+declared])
	return Frame{Type: buf[2], Flags: buf[3], Payload: payload}, total, nil
}

// EncodeFrame serializes one frame, enforcing the given payload quota.
func EncodeFrame(typ, flags byte, payload []byte, maxPayload int) ([]byte, error) {
	if len(payload) > maxPayload {
		return nil, fmt.Errorf("seqwin: payload %d exceeds quota %d", len(payload), maxPayload)
	}
	out := make([]byte, 0, HeaderLen+len(payload)+CrcLen)
	out = append(out, magic[0], magic[1], typ, flags, 0, 0)
	binary.BigEndian.PutUint16(out[4:6], uint16(len(payload)))
	out = append(out, payload...)
	crc := crc16(out[2:])
	return append(out, byte(crc>>8), byte(crc)), nil
}

// crc16 computes CRC-16/CCITT-FALSE (poly 0x1021, init 0xFFFF).
func crc16(data []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, b := range data {
		crc ^= uint16(b) << 8
		for i := 0; i < 8; i++ {
			if crc&0x8000 != 0 {
				crc = crc<<1 ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}
