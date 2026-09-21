// Package sframe implements a length-prefixed, CRC-protected binary frame
// format with a nested TLV payload, plus streaming parsing and field
// redaction that recomputes lengths and checksums.
//
// Frame layout (all integers big-endian):
//
//	0  ..1   magic "SF"
//	2        version (1)
//	3        flags
//	4  ..11  sequence number (uint64)   -- preserved for replay ordering
//	12 ..19  timestamp (unix nano)      -- preserved for timing
//	20 ..23  payload length (uint32)
//	24 ..    payload (TLV fields)
//	last 4   CRC32-IEEE over header+payload
package sframe

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
)

const (
	HeaderSize = 24
	CRCSize    = 4
	Version1   = 1

	magic0 = 'S'
	magic1 = 'F'
)

var errNeedMore = errors.New("sframe: incomplete data, need more bytes")

// NeedMore reports whether err means the input is truncated (more bytes may
// complete the frame) as opposed to corrupted.
func NeedMore(err error) bool { return errors.Is(err, errNeedMore) }

// CorruptError describes malformed input. It never carries payload content,
// only structural facts (offsets, lengths, type codes), so sensitive values
// cannot leak through logs.
type CorruptError struct{ Reason string }

func (e *CorruptError) Error() string { return "sframe: corrupt: " + e.Reason }

func corruptf(format string, args ...any) error {
	return &CorruptError{Reason: fmt.Sprintf(format, args...)}
}

// Header carries timing and ordering metadata. Redaction never modifies it,
// so redacted streams keep their original timing and remain replayable.
type Header struct {
	Version   uint8
	Flags     uint8
	Seq       uint64
	Timestamp int64
}

// EncodeFrame builds a complete frame from a header and payload.
func EncodeFrame(h Header, payload []byte) []byte {
	out := make([]byte, HeaderSize+len(payload)+CRCSize)
	out[0] = magic0
	out[1] = magic1
	out[2] = h.Version
	out[3] = h.Flags
	binary.BigEndian.PutUint64(out[4:12], h.Seq)
	binary.BigEndian.PutUint64(out[12:20], uint64(h.Timestamp))
	binary.BigEndian.PutUint32(out[20:24], uint32(len(payload)))
	copy(out[HeaderSize:], payload)
	crc := crc32.ChecksumIEEE(out[:HeaderSize+len(payload)])
	binary.BigEndian.PutUint32(out[HeaderSize+len(payload):], crc)
	return out
}

// DecodeFrame attempts to decode one frame from the front of buf.
// It returns the frame, the number of bytes consumed, and an error that is
// either "need more data" (see NeedMore) or *CorruptError.
//
// The payload-length quota is enforced before any allocation or wait: an
// over-quota length is reported as corruption immediately, even if the
// announced bytes have not arrived yet.
func DecodeFrame(buf []byte, maxPayload uint32) (frame []byte, consumed int, err error) {
	if len(buf) < HeaderSize {
		return nil, 0, errNeedMore
	}
	if buf[0] != magic0 || buf[1] != magic1 {
		return nil, 0, corruptf("bad magic 0x%02x%02x", buf[0], buf[1])
	}
	if buf[2] != Version1 {
		return nil, 0, corruptf("unsupported version %d", buf[2])
	}
	payloadLen := binary.BigEndian.Uint32(buf[20:24])
	if payloadLen > maxPayload {
		return nil, 0, corruptf("payload length %d exceeds quota %d", payloadLen, maxPayload)
	}
	total := HeaderSize + int(payloadLen) + CRCSize
	if len(buf) < total {
		return nil, 0, errNeedMore
	}
	body := buf[:total-CRCSize]
	wantCRC := binary.BigEndian.Uint32(buf[total-CRCSize : total])
	if crc32.ChecksumIEEE(body) != wantCRC {
		return nil, 0, corruptf("crc mismatch at frame of %d bytes", total)
	}
	frame = make([]byte, total) // allocation happens only after quota + CRC checks
	copy(frame, buf[:total])
	return frame, total, nil
}

// SplitFrame validates a complete frame and returns its header and payload.
func SplitFrame(frame []byte) (Header, []byte, error) {
	if len(frame) < HeaderSize+CRCSize {
		return Header{}, nil, corruptf("frame too short: %d bytes", len(frame))
	}
	if frame[0] != magic0 || frame[1] != magic1 {
		return Header{}, nil, corruptf("bad magic 0x%02x%02x", frame[0], frame[1])
	}
	if frame[2] != Version1 {
		return Header{}, nil, corruptf("unsupported version %d", frame[2])
	}
	payloadLen := binary.BigEndian.Uint32(frame[20:24])
	if int(payloadLen) != len(frame)-HeaderSize-CRCSize {
		return Header{}, nil, corruptf("declared payload length %d does not match frame size %d",
			payloadLen, len(frame))
	}
	body := frame[:len(frame)-CRCSize]
	wantCRC := binary.BigEndian.Uint32(frame[len(frame)-CRCSize:])
	if crc32.ChecksumIEEE(body) != wantCRC {
		return Header{}, nil, corruptf("crc mismatch")
	}
	h := Header{
		Version:   frame[2],
		Flags:     frame[3],
		Seq:       binary.BigEndian.Uint64(frame[4:12]),
		Timestamp: int64(binary.BigEndian.Uint64(frame[12:20])),
	}
	return h, frame[HeaderSize : len(frame)-CRCSize], nil
}

// Parser is an incremental frame decoder. Bytes may be fed in arbitrary
// chunks; truncation is reported by the absence of frames (no error), while
// malformed input yields a *CorruptError.
type Parser struct {
	buf        []byte
	maxPayload uint32
}

func NewParser(maxPayload uint32) *Parser { return &Parser{maxPayload: maxPayload} }

// Feed appends chunk to the internal buffer and returns every frame that
// became complete. A nil error with zero frames simply means more bytes are
// needed. On a *CorruptError the parser should be discarded.
func (p *Parser) Feed(chunk []byte) ([][]byte, error) {
	p.buf = append(p.buf, chunk...)
	var out [][]byte
	for {
		frame, n, err := DecodeFrame(p.buf, p.maxPayload)
		if err != nil {
			if errNeedMore == err {
				break
			}
			return nil, err
		}
		out = append(out, frame)
		p.buf = p.buf[n:]
	}
	if len(p.buf) == 0 {
		p.buf = nil
	}
	return out, nil
}
