// Package wire implements a zero-dependency, hand-written Protocol Buffers
// binary wire-format codec. It supports varint (7-bit compressed) encoding,
// ZigZag signed-integer transforms, fixed32/fixed64 and length-delimited
// (nested message / packed repeated) fields.
//
// Every read is bounds-checked: a truncated or malformed buffer is reported
// as ErrTruncated / ErrOverflow / ErrInvalidWireType and never causes an
// out-of-bounds memory access.
package wire

import (
	"errors"
	"fmt"
	"math"
)

// Wire types as defined by the Protocol Buffers binary encoding.
const (
	WireVarint     = 0 // int32, int64, uint32, uint64, sint32, sint64, bool, enum
	WireFixed64    = 1 // fixed64, sfixed64, double
	WireLengthPref = 2 // string, bytes, embedded messages, packed repeated fields
	WireFixed32    = 5 // fixed32, sfixed32, float
)

const (
	// MaxVarintBytes is the maximum encoded length of a 64-bit varint.
	MaxVarintBytes = 10
	// MaxFieldNumber is the largest legal protobuf field number (2^29 - 1).
	MaxFieldNumber = 1<<29 - 1
)

var (
	// ErrTruncated is returned when the buffer ends before a complete
	// element (key, varint, fixed value or length-delimited payload)
	// could be read. It signals a safe, detected truncation.
	ErrTruncated = errors.New("wire: truncated input, safe bounds guard triggered")
	// ErrOverflow is returned when a varint uses more than 10 bytes or
	// carries bits beyond 64.
	ErrOverflow = errors.New("wire: varint overflows 64 bits")
	// ErrInvalidWireType is returned for wire types 3, 4 (deprecated
	// groups), 6 and 7, which this codec rejects.
	ErrInvalidWireType = errors.New("wire: invalid or unsupported wire type")
	// ErrBadFieldNumber is returned for field number 0 or numbers above
	// MaxFieldNumber.
	ErrBadFieldNumber = errors.New("wire: illegal field number")
	// ErrNegativeLength is returned when a length-delimited prefix is
	// negative or exceeds the remaining input.
	ErrNegativeLength = errors.New("wire: invalid length-delimited size")
)

// ---------------------------------------------------------------------------
// Varint: 7-bit-per-byte little-endian base-128 compression
// ---------------------------------------------------------------------------

// AppendUvarint appends the varint encoding of v to buf and returns the
// extended buffer. Each byte carries 7 payload bits; the high bit is the
// continuation flag.
func AppendUvarint(buf []byte, v uint64) []byte {
	for v >= 0x80 {
		buf = append(buf, byte(v)|0x80)
		v >>= 7
	}
	return append(buf, byte(v))
}

// AppendVarint appends the two's-complement varint encoding of a signed
// value (negative values take the full 10 bytes, matching protobuf).
func AppendVarint(buf []byte, v int64) []byte {
	return AppendUvarint(buf, uint64(v))
}

// UvarintSize returns the encoded length of v in bytes.
func UvarintSize(v uint64) int {
	n := 1
	for v >= 0x80 {
		v >>= 7
		n++
	}
	return n
}

// ConsumeUvarint decodes a varint from buf starting at pos. It returns the
// value and the position just past the varint. Bounds-safe: returns
// ErrTruncated if the buffer ends mid-varint and ErrOverflow if the varint
// exceeds 64 bits.
func ConsumeUvarint(buf []byte, pos int) (uint64, int, error) {
	if pos < 0 || pos > len(buf) {
		return 0, pos, ErrTruncated
	}
	var v uint64
	for i := 0; i < MaxVarintBytes; i++ {
		if pos+i >= len(buf) {
			return 0, pos, ErrTruncated
		}
		b := buf[pos+i]
		if i == MaxVarintBytes-1 && b > 1 {
			return 0, pos, ErrOverflow
		}
		v |= uint64(b&0x7f) << uint(7*i)
		if b < 0x80 {
			return v, pos + i + 1, nil
		}
	}
	return 0, pos, ErrOverflow
}

// ConsumeVarint decodes a varint as a signed 64-bit two's-complement value.
func ConsumeVarint(buf []byte, pos int) (int64, int, error) {
	v, next, err := ConsumeUvarint(buf, pos)
	return int64(v), next, err
}

// ---------------------------------------------------------------------------
// ZigZag: signed integers mapped to unsigned for efficient varint coding
// ---------------------------------------------------------------------------

// EncodeZigZag64 maps a signed integer to an unsigned one so that values
// with small absolute magnitude have small encodings: 0->0, -1->1, 1->2.
func EncodeZigZag64(v int64) uint64 {
	return uint64(v<<1) ^ uint64(v>>63)
}

// DecodeZigZag64 is the inverse of EncodeZigZag64.
func DecodeZigZag64(v uint64) int64 {
	return int64(v>>1) ^ -int64(v&1)
}

// EncodeZigZag32 is the 32-bit ZigZag transform.
func EncodeZigZag32(v int32) uint32 {
	return uint32(v<<1) ^ uint32(v>>31)
}

// DecodeZigZag32 is the inverse of EncodeZigZag32.
func DecodeZigZag32(v uint32) int32 {
	return int32(v>>1) ^ -int32(v&1)
}

// ---------------------------------------------------------------------------
// Fixed32 / Fixed64 little-endian
// ---------------------------------------------------------------------------

// AppendFixed32 appends v as 4 little-endian bytes.
func AppendFixed32(buf []byte, v uint32) []byte {
	return append(buf, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

// AppendFixed64 appends v as 8 little-endian bytes.
func AppendFixed64(buf []byte, v uint64) []byte {
	return append(buf,
		byte(v), byte(v>>8), byte(v>>16), byte(v>>24),
		byte(v>>32), byte(v>>40), byte(v>>48), byte(v>>56))
}

// ConsumeFixed32 reads 4 little-endian bytes with bounds checking.
func ConsumeFixed32(buf []byte, pos int) (uint32, int, error) {
	if pos < 0 || pos+4 > len(buf) {
		return 0, pos, ErrTruncated
	}
	v := uint32(buf[pos]) | uint32(buf[pos+1])<<8 |
		uint32(buf[pos+2])<<16 | uint32(buf[pos+3])<<24
	return v, pos + 4, nil
}

// ConsumeFixed64 reads 8 little-endian bytes with bounds checking.
func ConsumeFixed64(buf []byte, pos int) (uint64, int, error) {
	if pos < 0 || pos+8 > len(buf) {
		return 0, pos, ErrTruncated
	}
	v := uint64(buf[pos]) | uint64(buf[pos+1])<<8 |
		uint64(buf[pos+2])<<16 | uint64(buf[pos+3])<<24 |
		uint64(buf[pos+4])<<32 | uint64(buf[pos+5])<<40 |
		uint64(buf[pos+6])<<48 | uint64(buf[pos+7])<<56
	return v, pos + 8, nil
}

// AppendFloat32 / AppendFloat64 encode IEEE-754 values as fixed32/fixed64.
func AppendFloat32(buf []byte, f float32) []byte {
	return AppendFixed32(buf, math.Float32bits(f))
}

func AppendFloat64(buf []byte, f float64) []byte {
	return AppendFixed64(buf, math.Float64bits(f))
}

// ---------------------------------------------------------------------------
// Field keys and length-delimited payloads
// ---------------------------------------------------------------------------

// AppendKey appends the tag key (fieldNumber<<3 | wireType) as a varint.
func AppendKey(buf []byte, fieldNumber int, wireType int) []byte {
	return AppendUvarint(buf, uint64(fieldNumber)<<3|uint64(wireType))
}

// ConsumeKey decodes a field key, validating the wire type and field number.
func ConsumeKey(buf []byte, pos int) (fieldNumber int, wireType int, next int, err error) {
	key, next, err := ConsumeUvarint(buf, pos)
	if err != nil {
		return 0, 0, pos, err
	}
	wireType = int(key & 0x7)
	switch wireType {
	case WireVarint, WireFixed64, WireLengthPref, WireFixed32:
	default:
		return 0, 0, pos, ErrInvalidWireType
	}
	fieldNumber = int(key >> 3)
	if fieldNumber <= 0 || fieldNumber > MaxFieldNumber {
		return 0, 0, pos, ErrBadFieldNumber
	}
	return fieldNumber, wireType, next, nil
}

// AppendBytes appends a length-delimited payload (uvarint length + bytes).
func AppendBytes(buf, payload []byte) []byte {
	buf = AppendUvarint(buf, uint64(len(payload)))
	return append(buf, payload...)
}

// AppendString appends a length-delimited string.
func AppendString(buf []byte, s string) []byte {
	return AppendBytes(buf, []byte(s))
}

// ConsumeBytes reads a length-delimited payload. The returned slice aliases
// buf. Bounds-safe: a declared length larger than the remaining input is
// reported as ErrTruncated, never as an out-of-bounds read.
func ConsumeBytes(buf []byte, pos int) ([]byte, int, error) {
	n, next, err := ConsumeUvarint(buf, pos)
	if err != nil {
		return nil, pos, err
	}
	if n > uint64(len(buf)-next) {
		return nil, pos, ErrTruncated
	}
	return buf[next : next+int(n) : next+int(n)], next + int(n), nil
}

// ConsumeString reads a length-delimited string (copies the bytes).
func ConsumeString(buf []byte, pos int) (string, int, error) {
	b, next, err := ConsumeBytes(buf, pos)
	if err != nil {
		return "", pos, err
	}
	return string(b), next, nil
}

// ---------------------------------------------------------------------------
// Low-level field walker
// ---------------------------------------------------------------------------

// Field is one raw decoded field header plus its payload view.
type Field struct {
	Number  int
	Type    int
	Varint  uint64 // valid when Type == WireVarint
	Fixed32 uint32 // valid when Type == WireFixed32
	Fixed64 uint64 // valid when Type == WireFixed64
	Bytes   []byte // valid when Type == WireLengthPref (aliases input)
}

// NextField decodes the field starting at buf[pos]. It returns the field and
// the position of the following field. pos == len(buf) yields done == true.
// All failures are reported as errors; no panic or out-of-bounds access is
// possible even on adversarial input.
func NextField(buf []byte, pos int) (f Field, next int, done bool, err error) {
	if pos < 0 || pos > len(buf) {
		return f, pos, false, ErrTruncated
	}
	if pos == len(buf) {
		return f, pos, true, nil
	}
	num, wt, p, err := ConsumeKey(buf, pos)
	if err != nil {
		return f, pos, false, err
	}
	f.Number, f.Type = num, wt
	switch wt {
	case WireVarint:
		f.Varint, p, err = ConsumeUvarint(buf, p)
	case WireFixed64:
		f.Fixed64, p, err = ConsumeFixed64(buf, p)
	case WireFixed32:
		f.Fixed32, p, err = ConsumeFixed32(buf, p)
	case WireLengthPref:
		f.Bytes, p, err = ConsumeBytes(buf, p)
	}
	if err != nil {
		return f, pos, false, err
	}
	return f, p, false, nil
}

// SkipValue advances past the value of a field whose key declared wireType,
// starting at payload position pos. Used to skip unknown fields safely.
func SkipValue(buf []byte, pos int, wireType int) (int, error) {
	switch wireType {
	case WireVarint:
		_, next, err := ConsumeUvarint(buf, pos)
		return next, err
	case WireFixed64:
		_, next, err := ConsumeFixed64(buf, pos)
		return next, err
	case WireFixed32:
		_, next, err := ConsumeFixed32(buf, pos)
		return next, err
	case WireLengthPref:
		_, next, err := ConsumeBytes(buf, pos)
		return next, err
	}
	return pos, ErrInvalidWireType
}

// ---------------------------------------------------------------------------
// Packed repeated helpers (length-delimited arrays of scalars)
// ---------------------------------------------------------------------------

// AppendPackedUvarints encodes values as one packed length-delimited body
// (no field key; the caller appends the key).
func AppendPackedUvarints(buf []byte, values []uint64) []byte {
	var body []byte
	for _, v := range values {
		body = AppendUvarint(body, v)
	}
	return AppendBytes(buf, body)
}

// ConsumePackedUvarints decodes a packed varint body of exactly len(buf)
// bytes. Any truncation inside the body is an error.
func ConsumePackedUvarints(buf []byte) ([]uint64, error) {
	var out []uint64
	pos := 0
	for pos < len(buf) {
		v, next, err := ConsumeUvarint(buf, pos)
		if err != nil {
			return nil, fmt.Errorf("wire: packed varint element: %v", err)
		}
		out = append(out, v)
		pos = next
	}
	return out, nil
}

// ConsumePackedFixed32 decodes a packed fixed32 body; the length must be a
// multiple of 4.
func ConsumePackedFixed32(buf []byte) ([]uint32, error) {
	if len(buf)%4 != 0 {
		return nil, fmt.Errorf("wire: packed fixed32 body length %d not a multiple of 4: %v", len(buf), ErrTruncated)
	}
	out := make([]uint32, 0, len(buf)/4)
	for pos := 0; pos < len(buf); {
		v, next, err := ConsumeFixed32(buf, pos)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
		pos = next
	}
	return out, nil
}

// ConsumePackedFixed64 decodes a packed fixed64 body; the length must be a
// multiple of 8.
func ConsumePackedFixed64(buf []byte) ([]uint64, error) {
	if len(buf)%8 != 0 {
		return nil, fmt.Errorf("wire: packed fixed64 body length %d not a multiple of 8: %v", len(buf), ErrTruncated)
	}
	out := make([]uint64, 0, len(buf)/8)
	for pos := 0; pos < len(buf); {
		v, next, err := ConsumeFixed64(buf, pos)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
		pos = next
	}
	return out, nil
}
