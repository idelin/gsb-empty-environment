// Package framearc implements a framed archive format with per-frame
// prefix coding and independent checksums, plus a resynchronizing reader.
package framearc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
)

// Magic marks the start of a frame header.
const Magic = "PCF1"

var magicBytes = []byte(Magic)

// Frame format (all integers big-endian):
//
//   offset  size  field
//   0       4     magic "PCF1"
//   4       4     seq
//   8       4     origLen   (decoded symbol count)
//   12      4     payLen    (compressed payload bytes)
//   16      2     tabLen    (number of code table entries)
//   18      2*tabLen  table entries: (symbol byte, code length byte)
//   ...     4     header CRC32 (over everything before it, magic included)
//   ...     payLen    payload (canonical prefix codes, MSB first, zero padded)
//   ...     4     payload CRC32 (over the decoded original bytes)

const (
	fixedHeaderLen = 18
	headerCRCLen   = 4
	payloadCRCLen  = 4

	maxTableLen = 256
	maxCodeLen  = 24

	// MaxOrigLen bounds the decoded size of a single frame.
	MaxOrigLen = 1 << 20
	// MaxPayLen bounds the compressed payload of a single frame.
	MaxPayLen = 1 << 20
)

var (
	errNeedMore       = errors.New("framearc: need more data")
	errNoMagic        = errors.New("framearc: no magic at offset")
	errBadField       = errors.New("framearc: header field out of range")
	errBadHeaderCRC   = errors.New("framearc: header crc mismatch")
	errBadTable       = errors.New("framearc: invalid code table")
	errTruncated      = errors.New("framearc: truncated payload")
	errInvalidCode    = errors.New("framearc: invalid code in payload")
	errNonZeroPadding = errors.New("framearc: non-zero padding bits")
	errPayloadCRC     = errors.New("framearc: payload crc mismatch")
)

type header struct {
	seq       uint32
	origLen   uint32
	payLen    uint32
	table     [][2]uint8
	headerLen int
}

func (h *header) frameLen() int {
	return h.headerLen + int(h.payLen) + payloadCRCLen
}

// parseHeader parses and validates a header at the start of buf.
// It returns errNeedMore if buf is too short to decide, errNoMagic if
// buf does not start with the magic, and a structural error otherwise.
func parseHeader(buf []byte) (*header, error) {
	if len(buf) < len(magicBytes) {
		return nil, errNeedMore
	}
	if !bytes.Equal(buf[:len(magicBytes)], magicBytes) {
		return nil, errNoMagic
	}
	if len(buf) < fixedHeaderLen {
		return nil, errNeedMore
	}
	seq := binary.BigEndian.Uint32(buf[4:8])
	origLen := binary.BigEndian.Uint32(buf[8:12])
	payLen := binary.BigEndian.Uint32(buf[12:16])
	tabLen := int(binary.BigEndian.Uint16(buf[16:18]))
	if origLen == 0 || origLen > MaxOrigLen {
		return nil, errBadField
	}
	if payLen == 0 || payLen > MaxPayLen {
		return nil, errBadField
	}
	if tabLen == 0 || tabLen > maxTableLen {
		return nil, errBadField
	}
	// origLen symbols need between origLen and origLen*maxCodeLen bits,
	// plus up to 7 padding bits in the final byte.
	if uint64(payLen)*8 < uint64(origLen) ||
		uint64(payLen)*8 > uint64(origLen)*maxCodeLen+7 {
		return nil, errBadField
	}
	headerLen := fixedHeaderLen + 2*tabLen + headerCRCLen
	if len(buf) < headerLen {
		return nil, errNeedMore
	}
	wantCRC := binary.BigEndian.Uint32(buf[headerLen-headerCRCLen : headerLen])
	if crc32.ChecksumIEEE(buf[:headerLen-headerCRCLen]) != wantCRC {
		return nil, errBadHeaderCRC
	}
	table := make([][2]uint8, tabLen)
	for i := 0; i < tabLen; i++ {
		table[i] = [2]uint8{buf[fixedHeaderLen+2*i], buf[fixedHeaderLen+2*i+1]}
	}
	if _, err := canonicalCodes(table); err != nil {
		return nil, err
	}
	return &header{
		seq:       seq,
		origLen:   origLen,
		payLen:    payLen,
		table:     table,
		headerLen: headerLen,
	}, nil
}

// decode decodes and verifies the payload of a complete frame.
// frame must be exactly frameLen() bytes.
func (h *header) decode(frame []byte) ([]byte, error) {
	payload := frame[h.headerLen : h.headerLen+int(h.payLen)]
	wantCRC := binary.BigEndian.Uint32(frame[h.headerLen+int(h.payLen):])
	entries, err := canonicalCodes(h.table)
	if err != nil {
		return nil, err
	}
	data, err := decodePayload(entries, payload, int(h.origLen))
	if err != nil {
		return nil, err
	}
	if crc32.ChecksumIEEE(data) != wantCRC {
		return nil, errPayloadCRC
	}
	return data, nil
}
