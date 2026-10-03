// Package wire implements a zero-dependency, hand-written parser and encoder
// for the Protocol Buffers binary wire format.
//
// Supported primitives:
//   - Varint (7-bit compressed unsigned integers, up to 64 bits)
//   - ZigZag signed integer mapping (sint32 / sint64)
//   - Fixed32 / Fixed64 (little-endian)
//   - Length-delimited fields, including nested messages and packed
//     repeated arrays
//
// All reads are bounds-checked: a truncated or malformed buffer always
// produces a typed error and never panics or reads out of range.
package wire

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// WireType is the 3-bit type tag carried by every field key.
type WireType int

const (
	WireVarint  WireType = 0 // int32, int64, uint32, uint64, sint32, sint64, bool, enum
	WireFixed64 WireType = 1 // fixed64, sfixed64, double
	WireBytes   WireType = 2 // string, bytes, embedded messages, packed repeated
	WireFixed32 WireType = 5 // fixed32, sfixed32, float
)

// Sentinel errors. Match them with errors.Is.
var (
	// ErrTruncated reports that the buffer ended before a value was complete.
	ErrTruncated = errors.New("wire: unexpected end of input")
	// ErrOverflow reports a varint that does not fit in 64 bits.
	ErrOverflow = errors.New("wire: varint overflows 64-bit integer")
	// ErrInvalidWireType reports an unsupported or reserved wire type.
	ErrInvalidWireType = errors.New("wire: invalid wire type")
	// ErrInvalidFieldNumber reports a field number outside [1, 2^29-1].
	ErrInvalidFieldNumber = errors.New("wire: invalid field number")
	// ErrLengthOverflow reports a length prefix that exceeds the input.
	ErrLengthOverflow = errors.New("wire: length prefix exceeds remaining input")
)

// maxVarintBytes is the maximum encoded length of a 64-bit varint.
const maxVarintBytes = 10

// maxFieldNumber is the largest field number allowed by the protobuf spec.
const maxFieldNumber = 1<<29 - 1

// Reader is a forward-only cursor over a wire-format buffer.
type Reader struct {
	buf []byte
	off int
}

// NewReader returns a Reader over buf. The buffer is not copied.
func NewReader(buf []byte) *Reader { return &Reader{buf: buf} }

// Remaining reports how many unread bytes are left.
func (r *Reader) Remaining() int { return len(r.buf) - r.off }

// Offset reports the current read position.
func (r *Reader) Offset() int { return r.off }

// ReadVarint reads one base-128 varint (7 payload bits per byte).
func (r *Reader) ReadVarint() (uint64, error) {
	var x uint64
	for i := 0; i < maxVarintBytes; i++ {
		if r.off >= len(r.buf) {
			return 0, ErrTruncated
		}
		b := r.buf[r.off]
		r.off++
		if i == maxVarintBytes-1 {
			// The 10th byte may carry only a single payload bit; anything
			// more (including a continuation bit) overflows uint64.
			if b > 1 {
				return 0, ErrOverflow
			}
		}
		x |= uint64(b&0x7f) << uint(7*i)
		if b < 0x80 {
			return x, nil
		}
	}
	return 0, ErrOverflow
}

// ReadZigZag64 reads a varint and undoes the ZigZag mapping (sint64).
func (r *Reader) ReadZigZag64() (int64, error) {
	v, err := r.ReadVarint()
	if err != nil {
		return 0, err
	}
	return DecodeZigZag64(v), nil
}

// ReadZigZag32 reads a varint and undoes the ZigZag mapping (sint32).
func (r *Reader) ReadZigZag32() (int32, error) {
	v, err := r.ReadVarint()
	if err != nil {
		return 0, err
	}
	return DecodeZigZag32(uint32(v)), nil
}

// ReadFixed32 reads 4 bytes in little-endian order.
func (r *Reader) ReadFixed32() (uint32, error) {
	if r.Remaining() < 4 {
		return 0, ErrTruncated
	}
	v := binary.LittleEndian.Uint32(r.buf[r.off:])
	r.off += 4
	return v, nil
}

// ReadFixed64 reads 8 bytes in little-endian order.
func (r *Reader) ReadFixed64() (uint64, error) {
	if r.Remaining() < 8 {
		return 0, ErrTruncated
	}
	v := binary.LittleEndian.Uint64(r.buf[r.off:])
	r.off += 8
	return v, nil
}

// ReadBytes reads a length-delimited byte slice. The returned slice aliases
// the underlying buffer; copy it if it must outlive the input.
func (r *Reader) ReadBytes() ([]byte, error) {
	n, err := r.ReadVarint()
	if err != nil {
		return nil, err
	}
	if n > uint64(r.Remaining()) {
		return nil, fmt.Errorf("%w: want %d bytes, have %d", ErrLengthOverflow, n, r.Remaining())
	}
	b := r.buf[r.off : r.off+int(n)]
	r.off += int(n)
	return b, nil
}

// ReadTag reads a field key and splits it into field number and wire type.
func (r *Reader) ReadTag() (num int, wt WireType, err error) {
	v, err := r.ReadVarint()
	if err != nil {
		return 0, 0, err
	}
	num = int(v >> 3)
	wt = WireType(v & 0x7)
	if num < 1 || num > maxFieldNumber {
		return 0, 0, fmt.Errorf("%w: %d", ErrInvalidFieldNumber, num)
	}
	switch wt {
	case WireVarint, WireFixed64, WireBytes, WireFixed32:
	default:
		return 0, 0, fmt.Errorf("%w: %d (groups are not supported)", ErrInvalidWireType, wt)
	}
	return num, wt, nil
}

// Field is one decoded field occurrence. Exactly one of the value slots is
// meaningful, selected by Type.
type Field struct {
	Number int
	Type   WireType
	U64    uint64 // WireVarint payload
	F32    uint32 // WireFixed32 payload
	F64    uint64 // WireFixed64 payload
	Bytes  []byte // WireBytes payload (aliases the input buffer)
}

// ZigZag64 interprets a varint field as sint64.
func (f Field) ZigZag64() int64 { return DecodeZigZag64(f.U64) }

// ZigZag32 interprets a varint field as sint32.
func (f Field) ZigZag32() int32 { return DecodeZigZag32(uint32(f.U64)) }

// Message is a decoded sequence of fields in wire order.
type Message struct {
	Fields []Field
}

// Get returns every occurrence of field number num, in wire order.
func (m *Message) Get(num int) []Field {
	var out []Field
	for _, f := range m.Fields {
		if f.Number == num {
			out = append(out, f)
		}
	}
	return out
}

// One returns the first occurrence of field num.
func (m *Message) One(num int) (Field, bool) {
	for _, f := range m.Fields {
		if f.Number == num {
			return f, true
		}
	}
	return Field{}, false
}

// ReadField decodes the next tag + value pair.
func (r *Reader) ReadField() (Field, error) {
	num, wt, err := r.ReadTag()
	if err != nil {
		return Field{}, err
	}
	f := Field{Number: num, Type: wt}
	switch wt {
	case WireVarint:
		f.U64, err = r.ReadVarint()
	case WireFixed64:
		f.F64, err = r.ReadFixed64()
	case WireBytes:
		f.Bytes, err = r.ReadBytes()
	case WireFixed32:
		f.F32, err = r.ReadFixed32()
	}
	if err != nil {
		return Field{}, fmt.Errorf("wire: field %d: %w", num, err)
	}
	return f, nil
}

// Decode parses a complete buffer into a Message. Every byte must be
// consumed by well-formed fields; trailing garbage is an error.
func Decode(buf []byte) (*Message, error) {
	r := NewReader(buf)
	m := &Message{}
	for r.Remaining() > 0 {
		f, err := r.ReadField()
		if err != nil {
			return nil, err
		}
		m.Fields = append(m.Fields, f)
	}
	return m, nil
}

// DecodeMessage parses a length-delimited payload as a nested message.
func DecodeMessage(b []byte) (*Message, error) { return Decode(b) }

// DecodePackedVarints decodes a packed repeated varint payload.
func DecodePackedVarints(b []byte) ([]uint64, error) {
	r := NewReader(b)
	var out []uint64
	for r.Remaining() > 0 {
		v, err := r.ReadVarint()
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// DecodePackedFixed32 decodes a packed repeated fixed32 payload.
func DecodePackedFixed32(b []byte) ([]uint32, error) {
	if len(b)%4 != 0 {
		return nil, fmt.Errorf("%w: packed fixed32 length %d not a multiple of 4", ErrTruncated, len(b))
	}
	out := make([]uint32, 0, len(b)/4)
	r := NewReader(b)
	for r.Remaining() > 0 {
		v, err := r.ReadFixed32()
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// DecodePackedFixed64 decodes a packed repeated fixed64 payload.
func DecodePackedFixed64(b []byte) ([]uint64, error) {
	if len(b)%8 != 0 {
		return nil, fmt.Errorf("%w: packed fixed64 length %d not a multiple of 8", ErrTruncated, len(b))
	}
	out := make([]uint64, 0, len(b)/8)
	r := NewReader(b)
	for r.Remaining() > 0 {
		v, err := r.ReadFixed64()
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// --- ZigZag mapping ---

// EncodeZigZag64 maps a signed int64 to its varint-friendly unsigned form.
func EncodeZigZag64(v int64) uint64 { return uint64(v<<1) ^ uint64(v>>63) }

// DecodeZigZag64 reverses EncodeZigZag64.
func DecodeZigZag64(v uint64) int64 { return int64(v>>1) ^ -int64(v&1) }

// EncodeZigZag32 maps a signed int32 to its varint-friendly unsigned form.
func EncodeZigZag32(v int32) uint32 { return uint32(v<<1) ^ uint32(v>>31) }

// DecodeZigZag32 reverses EncodeZigZag32.
func DecodeZigZag32(v uint32) int32 { return int32(v>>1) ^ -int32(v&1) }

// --- Encoding helpers (used by tests, examples and the demo) ---

// AppendVarint appends v in base-128 varint form.
func AppendVarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// AppendTag appends the key for field num with wire type wt.
func AppendTag(dst []byte, num int, wt WireType) []byte {
	return AppendVarint(dst, uint64(num)<<3|uint64(wt))
}

// AppendFixed32 appends v as 4 little-endian bytes.
func AppendFixed32(dst []byte, v uint32) []byte {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	return append(dst, b[:]...)
}

// AppendFixed64 appends v as 8 little-endian bytes.
func AppendFixed64(dst []byte, v uint64) []byte {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], v)
	return append(dst, b[:]...)
}

// AppendBytes appends a length-delimited byte slice.
func AppendBytes(dst, b []byte) []byte {
	dst = AppendVarint(dst, uint64(len(b)))
	return append(dst, b...)
}

// AppendString appends a length-delimited string field payload.
func AppendString(dst []byte, s string) []byte { return AppendBytes(dst, []byte(s)) }

// AppendZigZag64 appends a sint64 value (ZigZag + varint).
func AppendZigZag64(dst []byte, v int64) []byte { return AppendVarint(dst, EncodeZigZag64(v)) }

// AppendPackedVarints packs values and appends them as one length-delimited payload.
func AppendPackedVarints(dst []byte, vs ...uint64) []byte {
	var body []byte
	for _, v := range vs {
		body = AppendVarint(body, v)
	}
	return AppendBytes(dst, body)
}

// AppendPackedFixed32 packs values and appends them as one length-delimited payload.
func AppendPackedFixed32(dst []byte, vs ...uint32) []byte {
	var body []byte
	for _, v := range vs {
		body = AppendFixed32(body, v)
	}
	return AppendBytes(dst, body)
}

// AppendPackedFixed64 packs values and appends them as one length-delimited payload.
func AppendPackedFixed64(dst []byte, vs ...uint64) []byte {
	var body []byte
	for _, v := range vs {
		body = AppendFixed64(body, v)
	}
	return AppendBytes(dst, body)
}

// SizeVarint reports the encoded length of v as a varint.
func SizeVarint(v uint64) int {
	n := 1
	for v >= 0x80 {
		v >>= 7
		n++
	}
	return n
}
