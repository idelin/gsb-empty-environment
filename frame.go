package pcf

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
)

// Frame wire format (all integers little-endian):
//
//	offset 0   Magic[4]     "PCF1"
//	offset 4   Seq          uint32  sequence number
//	offset 8   RawLen       uint32  decoded payload length in bytes
//	offset 12  PayloadLen   uint32  encoded payload length in bytes
//	offset 16  Table[256]   prefix-code lengths, one byte per symbol
//	offset 272 Payload      PayloadLen bytes, MSB-first bit stream
//	...        CRC32        uint32  IEEE, over every preceding frame byte
//
// A frame therefore occupies HeaderSize + PayloadLen + CRCSize bytes.
var Magic = []byte{'P', 'C', 'F', '1'}

const (
	SeqOff        = 4
	RawLenOff     = 8
	PayloadLenOff = 12
	TableOff      = 16
	TableSize     = 256
	HeaderSize    = TableOff + TableSize
	CRCSize       = 4
)

// header is a parsed, structurally validated frame header.
type header struct {
	seq        uint32
	rawLen     uint32
	payloadLen uint32
	lengths    []byte // 256 bytes, aliases the frame buffer
}

// EncodeFrame builds a valid frame carrying data with the given sequence
// number. Empty data yields a frame with an empty table and no payload.
func EncodeFrame(seq uint32, data []byte) []byte {
	lengths := make([]byte, TableSize)
	var payload []byte
	if len(data) > 0 {
		var freqs [256]uint64
		for _, b := range data {
			freqs[b]++
		}
		lengths = buildCodeLengths(freqs[:])
		payload = encodePayload(data, lengths)
	}
	out := make([]byte, 0, HeaderSize+len(payload)+CRCSize)
	out = append(out, Magic...)
	var tmp [4]byte
	binary.LittleEndian.PutUint32(tmp[:], seq)
	out = append(out, tmp[:]...)
	binary.LittleEndian.PutUint32(tmp[:], uint32(len(data)))
	out = append(out, tmp[:]...)
	binary.LittleEndian.PutUint32(tmp[:], uint32(len(payload)))
	out = append(out, tmp[:]...)
	out = append(out, lengths...)
	out = append(out, payload...)
	binary.LittleEndian.PutUint32(tmp[:], crc32.ChecksumIEEE(out))
	out = append(out, tmp[:]...)
	return out
}

// parseHeader validates the structural fields of a frame header.
// buf must be at least HeaderSize bytes long and start with Magic.
func (r *Reader) parseHeader(buf []byte) (header, error) {
	var h header
	h.seq = binary.LittleEndian.Uint32(buf[SeqOff:])
	h.rawLen = binary.LittleEndian.Uint32(buf[RawLenOff:])
	h.payloadLen = binary.LittleEndian.Uint32(buf[PayloadLenOff:])
	h.lengths = buf[TableOff:HeaderSize]
	if h.rawLen > r.maxRaw {
		return h, fmt.Errorf("raw length %d exceeds limit %d", h.rawLen, r.maxRaw)
	}
	if h.payloadLen > r.maxPayload {
		return h, fmt.Errorf("payload length %d exceeds limit %d", h.payloadLen, r.maxPayload)
	}
	if h.rawLen == 0 {
		if h.payloadLen != 0 {
			return h, errors.New("empty frame with nonzero payload length")
		}
		for _, l := range h.lengths {
			if l != 0 {
				return h, errors.New("empty frame with nonempty code table")
			}
		}
		return h, nil
	}
	if h.payloadLen == 0 {
		return h, errors.New("nonempty frame with zero payload length")
	}
	// Every symbol costs between 1 and maxCodeLen bits, plus <8 padding bits.
	bits := uint64(h.payloadLen) * 8
	if bits < uint64(h.rawLen) || bits > uint64(h.rawLen)*maxCodeLen+7 {
		return h, fmt.Errorf("payload/bit length mismatch: raw=%d payload=%d", h.rawLen, h.payloadLen)
	}
	if err := validateCodeLengths(h.lengths); err != nil {
		return h, err
	}
	return h, nil
}
