// Package wire implements a zero-dependency, hand-rolled Protocol Buffers
// binary wire-format codec: Varint 7-bit compression, ZigZag signed integer
// transforms, Fixed32/64 and nested length-delimited messages.
//
// Every Consume* function is bounds-safe: on truncated input it returns
// ErrTruncated instead of reading out of range.
package wire

import "errors"

var (
	// ErrTruncated is returned when a buffer ends before a complete value
	// could be decoded. It is the safe out-of-bounds guard of the codec.
	ErrTruncated = errors.New("wire: truncated buffer, safe bounds guard triggered")
	// ErrOverflow is returned when a varint exceeds 64 bits (more than
	// 10 bytes, or a 10th byte carrying bits beyond bit 63).
	ErrOverflow = errors.New("wire: varint overflows 64 bits")
	// ErrInvalidWireType is returned for wire types outside {0,1,2,5}.
	ErrInvalidWireType = errors.New("wire: invalid wire type")
	// ErrInvalidFieldNumber is returned for field numbers < 1 or > 2^29-1.
	ErrInvalidFieldNumber = errors.New("wire: invalid field number")
	// ErrNegativeLength is returned when a length prefix is implausibly large.
	ErrNegativeLength = errors.New("wire: length prefix exceeds remaining buffer")
)

// MaxVarintLen is the maximum encoded length of a 64-bit varint.
const MaxVarintLen = 10

// Type is a protobuf wire type.
type Type int

const (
	VarintType  Type = 0 // int32, int64, uint32, uint64, sint32, sint64, bool, enum
	Fixed64Type Type = 1 // fixed64, sfixed64, double
	BytesType   Type = 2 // string, bytes, embedded messages, packed repeated
	Fixed32Type Type = 5 // fixed32, sfixed32, float
)

// Valid reports whether t is one of the four supported wire types.
func (t Type) Valid() bool {
	switch t {
	case VarintType, Fixed64Type, BytesType, Fixed32Type:
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// Varint: 7-bit little-endian group compression, MSB = continuation bit.
// ---------------------------------------------------------------------------

// AppendVarint encodes v as a varint and appends it to buf.
func AppendVarint(buf []byte, v uint64) []byte {
	for v >= 0x80 {
		buf = append(buf, byte(v)|0x80)
		v >>= 7
	}
	return append(buf, byte(v))
}

// SizeVarint returns the encoded length of v as a varint.
func SizeVarint(v uint64) int {
	n := 1
	for v >= 0x80 {
		v >>= 7
		n++
	}
	return n
}

// ConsumeVarint decodes a varint from the head of buf, returning the value
// and the number of bytes consumed. It never reads past len(buf).
func ConsumeVarint(buf []byte) (v uint64, n int, err error) {
	for i := 0; i < len(buf) && i < MaxVarintLen; i++ {
		b := buf[i]
		if i == MaxVarintLen-1 && b > 1 {
			// The 10th byte may only contribute bit 63.
			return 0, 0, ErrOverflow
		}
		v |= uint64(b&0x7f) << (uint(i) * 7)
		if b < 0x80 {
			return v, i + 1, nil
		}
	}
	if len(buf) >= MaxVarintLen {
		// 10 bytes all with continuation bit set: 11th byte required.
		return 0, 0, ErrOverflow
	}
	return 0, 0, ErrTruncated
}

// ---------------------------------------------------------------------------
// ZigZag: signed integers mapped to unsigned so small magnitudes stay small.
// ---------------------------------------------------------------------------

// EncodeZigZag64 maps a signed int64 to its zigzag unsigned form.
func EncodeZigZag64(v int64) uint64 { return uint64(v<<1) ^ uint64(v>>63) }

// DecodeZigZag64 is the inverse of EncodeZigZag64.
func DecodeZigZag64(v uint64) int64 { return int64(v>>1) ^ -int64(v&1) }

// EncodeZigZag32 maps a signed int32 to its zigzag unsigned form.
func EncodeZigZag32(v int32) uint32 { return uint32(v<<1) ^ uint32(v>>31) }

// DecodeZigZag32 is the inverse of EncodeZigZag32.
func DecodeZigZag32(v uint32) int32 { return int32(v>>1) ^ -int32(v&1) }

// AppendZigZag64 encodes a signed varint (sint64) onto buf.
func AppendZigZag64(buf []byte, v int64) []byte { return AppendVarint(buf, EncodeZigZag64(v)) }

// AppendZigZag32 encodes a signed varint (sint32) onto buf.
func AppendZigZag32(buf []byte, v int32) []byte { return AppendVarint(buf, uint64(EncodeZigZag32(v))) }

// ---------------------------------------------------------------------------
// Fixed32 / Fixed64: little-endian, no compression.
// ---------------------------------------------------------------------------

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
	return uint64(buf[0]) | uint64(buf[1])<<8 | uint64(buf[2])<<16 | uint64(buf[3])<<24 |
		uint64(buf[4])<<32 | uint64(buf[5])<<40 | uint64(buf[6])<<48 | uint64(buf[7])<<56, 8, nil
}

// ---------------------------------------------------------------------------
// Tags: (field_number << 3) | wire_type, itself a varint.
// ---------------------------------------------------------------------------

// MaxFieldNumber is the largest legal protobuf field number (2^29 - 1).
const MaxFieldNumber = 1<<29 - 1

// AppendTag encodes a field tag onto buf.
func AppendTag(buf []byte, num int, wt Type) []byte {
	return AppendVarint(buf, uint64(num)<<3|uint64(wt))
}

// ConsumeTag decodes a field tag from buf.
func ConsumeTag(buf []byte) (num int, wt Type, n int, err error) {
	v, n, err := ConsumeVarint(buf)
	if err != nil {
		return 0, 0, 0, err
	}
	num = int(v >> 3)
	wt = Type(v & 7)
	if num < 1 || num > MaxFieldNumber {
		return 0, 0, 0, ErrInvalidFieldNumber
	}
	if !wt.Valid() {
		return 0, 0, 0, ErrInvalidWireType
	}
	return num, wt, n, nil
}

// ---------------------------------------------------------------------------
// Length-delimited: varint length prefix followed by raw payload.
// ---------------------------------------------------------------------------

// AppendBytes appends b with a varint length prefix.
func AppendBytes(buf, b []byte) []byte {
	buf = AppendVarint(buf, uint64(len(b)))
	return append(buf, b...)
}

// ConsumeBytes reads a length-prefixed byte slice from buf. The returned
// slice aliases buf; copy it if it must outlive the source buffer.
func ConsumeBytes(buf []byte) (b []byte, n int, err error) {
	l, m, err := ConsumeVarint(buf)
	if err != nil {
		return nil, 0, err
	}
	if l > uint64(len(buf)-m) {
		return nil, 0, ErrNegativeLength
	}
	return buf[m : m+int(l) : m+int(l)], m + int(l), nil
}
