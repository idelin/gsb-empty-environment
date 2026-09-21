package reassemble

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"time"
)

// Wire format, fixed-size 31-byte header followed by the payload:
//
//	[0:2]   magic 'F','G'
//	[2]     version (= 1)
//	[3:11]  message ID, uint64 big-endian
//	[11:15] total message length, uint32 big-endian
//	[15:19] fragment offset, uint32 big-endian
//	[19:23] payload length, uint32 big-endian
//	[23:27] CRC32-IEEE of the payload
//	[27:31] CRC32-IEEE of header bytes [0:27]
//	[31:]   payload
const HeaderSize = 31

const frameVersion = 1

var frameMagic = [2]byte{'F', 'G'}

// BadDataError reports malformed input (bad magic, version, checksum, or
// implausible lengths). It is distinct from simply not having enough bytes
// yet: an incomplete trailing frame is buffered, never an error.
type BadDataError struct{ Reason string }

func (e *BadDataError) Error() string { return "reassemble: bad data: " + e.Reason }

// IsBadData reports whether err is a BadDataError.
func IsBadData(err error) bool {
	var b *BadDataError
	return errors.As(err, &b)
}

// Frame is one parsed fragment.
type Frame struct {
	ID       uint64
	TotalLen uint32
	Offset   uint32
	Payload  []byte
}

// Parser turns an arbitrarily chunked byte stream into frames. Bytes may be
// fed in any size pieces; a partial header or payload is kept until more
// bytes arrive (need more data), while malformed input yields a
// *BadDataError (bad data).
type Parser struct {
	buf []byte
	// MaxPayload caps a single frame's payload. It is checked before any
	// payload buffer is allocated.
	MaxPayload uint32
}

// NewParser returns a Parser rejecting payloads larger than maxPayload.
func NewParser(maxPayload uint32) *Parser { return &Parser{MaxPayload: maxPayload} }

// Write consumes b and returns every frame completed by it. A trailing
// partial frame is not an error; malformed bytes are.
func (p *Parser) Write(b []byte) ([]Frame, error) {
	p.buf = append(p.buf, b...)
	var frames []Frame
	for {
		if len(p.buf) < HeaderSize {
			break // need more data
		}
		hdr := p.buf[:HeaderSize]
		if hdr[0] != frameMagic[0] || hdr[1] != frameMagic[1] {
			return frames, &BadDataError{Reason: "bad magic"}
		}
		if hdr[2] != frameVersion {
			return frames, &BadDataError{Reason: "unsupported version"}
		}
		if crc32.ChecksumIEEE(hdr[:27]) != binary.BigEndian.Uint32(hdr[27:31]) {
			return frames, &BadDataError{Reason: "header checksum mismatch"}
		}
		totalLen := binary.BigEndian.Uint32(hdr[11:15])
		offset := binary.BigEndian.Uint32(hdr[15:19])
		payloadLen := binary.BigEndian.Uint32(hdr[19:23])
		// Length/quota checks happen before allocating the payload copy.
		if payloadLen > p.MaxPayload {
			return frames, &BadDataError{Reason: "payload length exceeds limit"}
		}
		if uint64(offset)+uint64(payloadLen) > uint64(totalLen) {
			return frames, &BadDataError{Reason: "fragment exceeds total length"}
		}
		if len(p.buf) < HeaderSize+int(payloadLen) {
			break // need more data
		}
		raw := p.buf[HeaderSize : HeaderSize+payloadLen]
		if crc32.ChecksumIEEE(raw) != binary.BigEndian.Uint32(hdr[23:27]) {
			return frames, &BadDataError{Reason: "payload checksum mismatch"}
		}
		payload := make([]byte, payloadLen)
		copy(payload, raw)
		frames = append(frames, Frame{
			ID:       binary.BigEndian.Uint64(hdr[3:11]),
			TotalLen: totalLen,
			Offset:   offset,
			Payload:  payload,
		})
		p.buf = p.buf[HeaderSize+payloadLen:]
	}
	if len(p.buf) == 0 {
		p.buf = nil
	}
	return frames, nil
}

// Pending reports the buffered bytes of an incomplete trailing frame.
func (p *Parser) Pending() int { return len(p.buf) }

// AppendFrame encodes one fragment onto dst and returns the extended slice.
func AppendFrame(dst []byte, id uint64, totalLen, offset uint32, payload []byte) []byte {
	hdr := make([]byte, HeaderSize)
	hdr[0], hdr[1], hdr[2] = frameMagic[0], frameMagic[1], frameVersion
	binary.BigEndian.PutUint64(hdr[3:11], id)
	binary.BigEndian.PutUint32(hdr[11:15], totalLen)
	binary.BigEndian.PutUint32(hdr[15:19], offset)
	binary.BigEndian.PutUint32(hdr[19:23], uint32(len(payload)))
	binary.BigEndian.PutUint32(hdr[23:27], crc32.ChecksumIEEE(payload))
	binary.BigEndian.PutUint32(hdr[27:31], crc32.ChecksumIEEE(hdr[:27]))
	dst = append(dst, hdr...)
	return append(dst, payload...)
}

// Handler couples a Parser with a Reassembler: bytes in, whole messages out.
type Handler struct {
	Parser *Parser
	Reasm  *Reassembler
}

// NewHandler builds a Handler from a Config and optional clock.
func NewHandler(cfg Config, now func() time.Time) *Handler {
	return &Handler{
		Parser: NewParser(uint32(cfg.MaxMessageSize)),
		Reasm:  New(cfg, now),
	}
}

// Write parses b and feeds every frame to the reassembler, returning each
// message that completed during this call.
func (h *Handler) Write(b []byte) ([][]byte, error) {
	frames, err := h.Parser.Write(b)
	var done [][]byte
	for _, f := range frames {
		data, complete, aerr := h.Reasm.Add(f.ID, int(f.TotalLen), int(f.Offset), f.Payload)
		if aerr != nil {
			return done, aerr
		}
		if complete && data != nil {
			done = append(done, data)
		}
	}
	return done, err
}
