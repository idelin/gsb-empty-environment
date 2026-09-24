// Package wiremsg implements a schema-driven TLV wire format that preserves
// unknown fields byte-for-byte.
//
// Supported serialization scope (the entire format, nothing else is valid):
//
//	field  := tag payload
//	tag    := varint(fieldNumber<<3 | wireType)   // fieldNumber >= 1
//	wireType 0 (varint)  -> uint64            payload: varint
//	wireType 5 (fixed32) -> uint32            payload: 4 bytes LE
//	wireType 1 (fixed64) -> uint64            payload: 8 bytes LE
//	wireType 2 (bytes)   -> []byte | string | nested message
//	                                        payload: varint(length) + length bytes
//
// Groups, zigzag varints, floats, maps and extensions are out of scope and
// rejected on decode. Decoding never executes host code: it is pure table
// driven byte parsing, and every length prefix is validated against the
// remaining input before any slice or allocation is made.
package wiremsg

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	WireVarint  = 0
	WireFixed64 = 1
	WireBytes   = 2
	WireFixed32 = 5
)

// ErrOverflow is returned when a varint exceeds 64 bits or 10 bytes.
var ErrOverflow = errors.New("wiremsg: varint overflow")

// ErrTruncated is returned when the input ends before a value is complete.
var ErrTruncated = errors.New("wiremsg: truncated input")

func appendVarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// readVarint decodes a varint from data[off:]. It validates before consuming.
func readVarint(data []byte, off int) (uint64, int, error) {
	var v uint64
	for i := 0; i < 10; i++ {
		if off+i >= len(data) {
			return 0, 0, ErrTruncated
		}
		b := data[off+i]
		if i == 9 && b > 1 {
			return 0, 0, ErrOverflow
		}
		v |= uint64(b&0x7f) << (7 * i)
		if b < 0x80 {
			return v, i + 1, nil
		}
	}
	return 0, 0, ErrOverflow
}

func appendTag(dst []byte, num uint32, wt int) []byte {
	return appendVarint(dst, uint64(num)<<3|uint64(wt))
}

// readTag parses a tag at data[off:] and returns field number, wire type and
// bytes consumed. Field number 0 and unknown wire types are rejected.
func readTag(data []byte, off int) (num uint32, wt int, n int, err error) {
	tag, n, err := readVarint(data, off)
	if err != nil {
		return 0, 0, 0, err
	}
	num = uint32(tag >> 3)
	wt = int(tag & 7)
	if num == 0 {
		return 0, 0, 0, errors.New("wiremsg: field number 0 is invalid")
	}
	switch wt {
	case WireVarint, WireFixed64, WireBytes, WireFixed32:
	default:
		return 0, 0, 0, fmt.Errorf("wiremsg: unsupported wire type %d", wt)
	}
	return num, wt, n, nil
}

// payloadLen returns the payload length of a value of wire type wt starting
// at data[off:], validating any length prefix against the remaining input
// before reporting it. No allocation happens here.
func payloadLen(wt int, data []byte, off int) (int, error) {
	switch wt {
	case WireVarint:
		_, n, err := readVarint(data, off)
		return n, err
	case WireFixed64:
		if len(data)-off < 8 {
			return 0, ErrTruncated
		}
		return 8, nil
	case WireFixed32:
		if len(data)-off < 4 {
			return 0, ErrTruncated
		}
		return 4, nil
	case WireBytes:
		l, n, err := readVarint(data, off)
		if err != nil {
			return 0, err
		}
		if l > uint64(len(data)-off-n) {
			return 0, fmt.Errorf("wiremsg: length %d exceeds remaining %d bytes: %w",
				l, len(data)-off-n, ErrTruncated)
		}
		return n + int(l), nil
	}
	return 0, fmt.Errorf("wiremsg: unsupported wire type %d", wt)
}

func appendFixed32(dst []byte, v uint32) []byte {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	return append(dst, b[:]...)
}

func appendFixed64(dst []byte, v uint64) []byte {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], v)
	return append(dst, b[:]...)
}
