// Package compactidx implements a read-only compact index over an encoded
// byte image. The image is a real serialized layout (header, offset table,
// chained records), not an in-memory map: queries are served by binary
// search over the offset table directly against the mapped bytes.
//
// Byte layout (all integers little-endian):
//
//	Header (HeaderLen = 48 bytes):
//	  0   magic[4]    "CIDX"
//	  4   version     uint16 (= Version)
//	  6   flags       uint16 (must be 0)
//	  8   headerLen   uint32 (= HeaderLen)
//	  12  entryCount  uint32
//	  16  tableOff    uint64  absolute offset of the offset table
//	  24  dataOff     uint64  absolute offset of the data region
//	  32  dataLen     uint64  length of the data region in bytes
//	  40  reserved    uint64  (must be 0)
//
//	Offset table at tableOff: entryCount * 8 bytes, each a uint64 absolute
//	offset of a head record inside [dataOff, dataOff+dataLen).
//
//	Record (inside the data region):
//	  uvarint keyLen   (0 only for continuation chunks)
//	  key     [keyLen]byte
//	  uvarint valLen
//	  val     [valLen]byte
//	  linkTag byte      0x00 = end of chain
//	                    0x01 = followed by uint64 absolute offset of the
//	                           continuation record (keyLen must be 0 there)
//
// A logical value is the concatenation of the val chunks along its chain.
package compactidx

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	HeaderLen      = 48
	Version        = 1
	tableEntrySize = 8

	linkNone = 0x00
	linkMore = 0x01

	maxUvarintLen = 10
)

var magic = [4]byte{'C', 'I', 'D', 'X'}

var (
	ErrEmptyFile       = errors.New("compactidx: empty file is not a valid empty index")
	ErrTruncated       = errors.New("compactidx: truncated file")
	ErrBadMagic        = errors.New("compactidx: bad magic bytes")
	ErrBadVersion      = errors.New("compactidx: unsupported format version")
	ErrOverflow        = errors.New("compactidx: integer overflow in offset arithmetic")
	ErrOffsetOutOfFile = errors.New("compactidx: offset points outside the file")
	ErrRegionOverlap   = errors.New("compactidx: overlapping regions in offset graph")
	ErrCycle           = errors.New("compactidx: cycle or duplicate reference in offset graph")
	ErrBadRecord       = errors.New("compactidx: malformed record")
	ErrUnsortedKeys    = errors.New("compactidx: head-record keys not strictly increasing")
)

type header struct {
	entryCount uint32
	tableOff   uint64
	dataOff    uint64
	dataLen    uint64
}

func (h header) encode(dst []byte) {
	copy(dst[0:4], magic[:])
	binary.LittleEndian.PutUint16(dst[4:6], Version)
	binary.LittleEndian.PutUint16(dst[6:8], 0)
	binary.LittleEndian.PutUint32(dst[8:12], HeaderLen)
	binary.LittleEndian.PutUint32(dst[12:16], h.entryCount)
	binary.LittleEndian.PutUint64(dst[16:24], h.tableOff)
	binary.LittleEndian.PutUint64(dst[24:32], h.dataOff)
	binary.LittleEndian.PutUint64(dst[32:40], h.dataLen)
	binary.LittleEndian.PutUint64(dst[40:48], 0)
}

func parseHeader(data []byte) (header, error) {
	var h header
	if len(data) == 0 {
		return h, ErrEmptyFile
	}
	if len(data) < HeaderLen {
		return h, fmt.Errorf("%w: header needs %d bytes, have %d", ErrTruncated, HeaderLen, len(data))
	}
	if string(data[0:4]) != string(magic[:]) {
		return h, ErrBadMagic
	}
	if v := binary.LittleEndian.Uint16(data[4:6]); v != Version {
		return h, fmt.Errorf("%w: %d", ErrBadVersion, v)
	}
	if binary.LittleEndian.Uint16(data[6:8]) != 0 {
		return h, fmt.Errorf("%w: flags must be zero", ErrBadRecord)
	}
	if n := binary.LittleEndian.Uint32(data[8:12]); n != HeaderLen {
		return h, fmt.Errorf("%w: headerLen=%d, want %d", ErrBadRecord, n, HeaderLen)
	}
	if binary.LittleEndian.Uint64(data[40:48]) != 0 {
		return h, fmt.Errorf("%w: reserved header bytes must be zero", ErrBadRecord)
	}
	h.entryCount = binary.LittleEndian.Uint32(data[12:16])
	h.tableOff = binary.LittleEndian.Uint64(data[16:24])
	h.dataOff = binary.LittleEndian.Uint64(data[24:32])
	h.dataLen = binary.LittleEndian.Uint64(data[32:40])
	return h, nil
}

// add64 returns a+b, reporting overflow.
func add64(a, b uint64) (uint64, error) {
	s := a + b
	if s < a {
		return 0, ErrOverflow
	}
	return s, nil
}

// mul64 returns a*b, reporting overflow.
func mul64(a, b uint64) (uint64, error) {
	if a != 0 && b > ^uint64(0)/a {
		return 0, ErrOverflow
	}
	return a * b, nil
}

// readUvarintRaw decodes a uvarint from data[off:], never reading at or past
// limit. It returns the value and the offset just past the varint.
func readUvarintRaw(data []byte, off, limit uint64) (uint64, uint64, error) {
	if off >= limit {
		return 0, 0, fmt.Errorf("%w: varint at offset %d", ErrTruncated, off)
	}
	end := off + maxUvarintLen
	if end > limit {
		end = limit
	}
	v, n := binary.Uvarint(data[off:end])
	if n == 0 {
		return 0, 0, fmt.Errorf("%w: unterminated varint at offset %d", ErrTruncated, off)
	}
	if n < 0 {
		return 0, 0, fmt.Errorf("%w: varint overflows uint64 at offset %d", ErrBadRecord, off)
	}
	return v, off + uint64(n), nil
}
