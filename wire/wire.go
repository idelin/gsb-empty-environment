// Package wire implements a zero-dependency Protocol Buffers binary
// wire-format codec: varint (7-bit compression), zigzag signed integers,
// fixed32/64 and length-delimited fields, all with strict bounds checking.
package wire

import "errors"

// WireType is the 3-bit type tag of a protobuf field.
type WireType int

const (
	WireVarint          WireType = 0
	WireFixed64         WireType = 1
	WireLengthDelimited WireType = 2
	WireFixed32         WireType = 5
)

var (
	ErrTruncated           = errors.New("wire: truncated input, safe bounds guard triggered")
	ErrOverflow            = errors.New("wire: varint overflows 64 bits")
	ErrInvalidWireType     = errors.New("wire: invalid wire type")
	ErrInvalidFieldNumber  = errors.New("wire: invalid field number")
	ErrInvalidPackedLength = errors.New("wire: packed element payload has invalid length")
)

// AppendVarint appends v to buf using 7-bit-per-byte varint encoding.
func AppendVarint(buf []byte, v uint64) []byte {
	for v >= 0x80 {
		buf = append(buf, byte(v)|0x80)
		v >>= 7
	}
	return append(buf, byte(v))
}

// ConsumeVarint decodes a varint from the head of buf, returning the value
// and the number of bytes consumed. ErrTruncated is returned when buf ends
// mid-varint; ErrOverflow when the encoding exceeds 64 bits (10 bytes).
func ConsumeVarint(buf []byte) (uint64, int, error) {
	var v uint64
	for i := 0; i < len(buf) && i < 10; i++ {
		b := buf[i]
		if i == 9 && b > 1 {
			return 0, 0, ErrOverflow
		}
		v |= uint64(b&0x7f) << (7 * i)
		if b < 0x80 {
			return v, i + 1, nil
		}
	}
	if len(buf) >= 10 {
		return 0, 0, ErrOverflow
	}
	return 0, 0, ErrTruncated
}

// EncodeZigZag maps a signed integer to an unsigned one so small-magnitude
// negatives stay compact: 0->0, -1->1, 1->2, -2->3, ...
func EncodeZigZag(v int64) uint64 {
	return uint64(v<<1) ^ uint64(v>>63)
}

// DecodeZigZag is the inverse of EncodeZigZag.
func DecodeZigZag(v uint64) int64 {
	return int64(v>>1) ^ -int64(v&1)
}

// AppendFixed32 appends v as 4 little-endian bytes.
func AppendFixed32(buf []byte, v uint32) []byte {
	return append(buf, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

// ConsumeFixed32 reads 4 little-endian bytes from buf.
func ConsumeFixed32(buf []byte) (uint32, int, error) {
	if len(buf) < 4 {
		return 0, 0, ErrTruncated
	}
	return uint32(buf[0]) | uint32(buf[1])<<8 | uint32(buf[2])<<16 | uint32(buf[3])<<24, 4, nil
}

// AppendFixed64 appends v as 8 little-endian bytes.
func AppendFixed64(buf []byte, v uint64) []byte {
	return append(buf,
		byte(v), byte(v>>8), byte(v>>16), byte(v>>24),
		byte(v>>32), byte(v>>40), byte(v>>48), byte(v>>56))
}

// ConsumeFixed64 reads 8 little-endian bytes from buf.
func ConsumeFixed64(buf []byte) (uint64, int, error) {
	if len(buf) < 8 {
		return 0, 0, ErrTruncated
	}
	v := uint64(buf[0]) | uint64(buf[1])<<8 | uint64(buf[2])<<16 | uint64(buf[3])<<24 |
		uint64(buf[4])<<32 | uint64(buf[5])<<40 | uint64(buf[6])<<48 | uint64(buf[7])<<56
	return v, 8, nil
}

// MakeTag builds the (field_number << 3 | wire_type) key varint.
func MakeTag(fieldNum int, wt WireType) uint64 {
	return uint64(fieldNum)<<3 | uint64(wt)
}

// SplitTag decomposes a tag varint into field number and wire type,
// rejecting field number 0 and reserved wire types 3, 4, 6, 7.
func SplitTag(tag uint64) (int, WireType, error) {
	num := int(tag >> 3)
	wt := WireType(tag & 0x7)
	if num < 1 {
		return 0, 0, ErrInvalidFieldNumber
	}
	switch wt {
	case WireVarint, WireFixed64, WireLengthDelimited, WireFixed32:
		return num, wt, nil
	default:
		return 0, 0, ErrInvalidWireType
	}
}

// AppendTag appends an encoded field key.
func AppendTag(buf []byte, fieldNum int, wt WireType) []byte {
	return AppendVarint(buf, MakeTag(fieldNum, wt))
}

// AppendBytes appends a length-delimited payload (varint length prefix + data).
func AppendBytes(buf, payload []byte) []byte {
	buf = AppendVarint(buf, uint64(len(payload)))
	return append(buf, payload...)
}

// ConsumeBytes reads a length-delimited payload. It never slices past the
// end of buf: a declared length larger than the remaining input yields
// ErrTruncated (safe out-of-bounds guard).
func ConsumeBytes(buf []byte) ([]byte, int, error) {
	l, n, err := ConsumeVarint(buf)
	if err != nil {
		return nil, 0, err
	}
	if l > uint64(len(buf)-n) {
		return nil, 0, ErrTruncated
	}
	return buf[n : n+int(l)], n + int(l), nil
}
