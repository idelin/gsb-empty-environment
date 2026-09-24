// Package wiremsg implements a schema-driven binary TLV message codec that
// preserves unknown fields byte-for-byte.
//
// Wire format (self-defined, protobuf-like):
//
//	field := tag value
//	tag   := varint(field_number<<3 | wire_type)
//
// Wire types: 0=varint, 1=fixed64, 2=length-delimited, 5=fixed32.
//
// Decoding is pure data processing: it never executes host code, and every
// declared length is validated against the remaining input before any
// allocation or slicing happens.
package wiremsg

import "errors"

const (
	WireVarint  = 0
	WireFixed64 = 1
	WireBytes   = 2
	WireFixed32 = 5
)

var (
	ErrTruncated   = errors.New("wiremsg: truncated input")
	ErrBadVarint   = errors.New("wiremsg: varint overflow")
	ErrBadWireType = errors.New("wiremsg: unsupported wire type")
	ErrBadTag      = errors.New("wiremsg: field number must be >= 1")
	ErrLength      = errors.New("wiremsg: declared length exceeds remaining input")
)

func appendVarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

func consumeVarint(buf []byte) (uint64, int, error) {
	var v uint64
	for i := 0; i < len(buf) && i < 10; i++ {
		b := buf[i]
		if b < 0x80 {
			if i == 9 && b > 1 {
				return 0, 0, ErrBadVarint
			}
			return v | uint64(b)<<uint(7*i), i + 1, nil
		}
		v |= uint64(b&0x7f) << uint(7*i)
	}
	if len(buf) < 10 {
		return 0, 0, ErrTruncated
	}
	return 0, 0, ErrBadVarint
}

// skipValue returns the end offset of the value starting at off, validating
// lengths without allocating anything.
func skipValue(data []byte, off int, wt int) (int, error) {
	switch wt {
	case WireVarint:
		_, n, err := consumeVarint(data[off:])
		return off + n, err
	case WireFixed64:
		if len(data)-off < 8 {
			return 0, ErrTruncated
		}
		return off + 8, nil
	case WireFixed32:
		if len(data)-off < 4 {
			return 0, ErrTruncated
		}
		return off + 4, nil
	case WireBytes:
		l, n, err := consumeVarint(data[off:])
		if err != nil {
			return 0, err
		}
		// Validate the declared length before touching the payload.
		if l > uint64(len(data)-off-n) {
			return 0, ErrLength
		}
		return off + n + int(l), nil
	default:
		return 0, ErrBadWireType
	}
}
