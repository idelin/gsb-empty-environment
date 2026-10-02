// Package wire implements a zero-dependency, hand-written Protocol Buffers
// binary wire-format parser and encoder.
//
// Supported wire types:
//   - Varint (type 0): 7-bit-per-byte compressed unsigned integers
//   - Fixed64 (type 1): little-endian 8-byte values
//   - Length-delimited (type 2): nested messages, strings, packed repeated
//   - Fixed32 (type 5): little-endian 4-byte values
//
// All reads are bounds-checked; truncated input is reported as ErrTruncated
// and never causes an out-of-bounds access.
package wire

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// WireType is the 3-bit wire type of a protobuf field.
type WireType int

const (
	Varint     WireType = 0
	Fixed64    WireType = 1
	Bytes      WireType = 2
	StartGroup WireType = 3 // recognized but unsupported
	EndGroup   WireType = 4 // recognized but unsupported
	Fixed32    WireType = 5
)

var (
	// ErrTruncated is returned when the buffer ends before a value is complete.
	ErrTruncated = errors.New("wire: truncated input")
	// ErrOverflow is returned when a varint exceeds 64 bits (>10 bytes).
	ErrOverflow = errors.New("wire: varint overflows 64 bits")
	// ErrInvalidWireType is returned for reserved/unsupported wire types.
	ErrInvalidWireType = errors.New("wire: invalid wire type")
	// ErrInvalidFieldNumber is returned for field number 0 or negative.
	ErrInvalidFieldNumber = errors.New("wire: invalid field number")
)

// MaxVarintLen is the maximum encoded length of a 64-bit varint.
const MaxVarintLen = 10

// Reader is a safe cursor over a protobuf-encoded buffer.
type Reader struct {
	buf []byte
	pos int
}

// NewReader returns a Reader over buf. The buffer is not copied.
func NewReader(buf []byte) *Reader { return &Reader{buf: buf} }

// Remaining reports how many bytes are left to read.
func (r *Reader) Remaining() int { return len(r.buf) - r.pos }

// Done reports whether the buffer has been fully consumed.
func (r *Reader) Done() bool { return r.pos >= len(r.buf) }

// Pos returns the current read offset.
func (r *Reader) Pos() int { return r.pos }

// ReadVarint reads a base-128 varint (7 bits per byte, LSB first).
// It returns ErrTruncated if the buffer ends mid-varint and ErrOverflow
// if the varint uses more than 10 bytes.
func (r *Reader) ReadVarint() (uint64, error) {
	var v uint64
	for i := 0; i < MaxVarintLen; i++ {
		if r.pos >= len(r.buf) {
			return 0, ErrTruncated
		}
		b := r.buf[r.pos]
		r.pos++
		if i == MaxVarintLen-1 {
			// 10th byte may only carry a single payload bit.
			if b > 1 {
				return 0, ErrOverflow
			}
		}
		v |= uint64(b&0x7f) << (7 * i)
		if b < 0x80 {
			return v, nil
		}
	}
	return 0, ErrOverflow
}

// ReadZigZag64 reads a varint and decodes it as a zigzag-encoded sint64.
func (r *Reader) ReadZigZag64() (int64, error) {
	v, err := r.ReadVarint()
	if err != nil {
		return 0, err
	}
	return int64(v>>1) ^ -int64(v&1), nil
}

// ReadZigZag32 reads a varint and decodes it as a zigzag-encoded sint32.
func (r *Reader) ReadZigZag32() (int32, error) {
	v, err := r.ReadVarint()
	if err != nil {
		return 0, err
	}
	return int32(int32(v>>1) ^ -int32(v&1)), nil
}

// ReadFixed32 reads a 4-byte little-endian value.
func (r *Reader) ReadFixed32() (uint32, error) {
	if r.Remaining() < 4 {
		return 0, ErrTruncated
	}
	v := binary.LittleEndian.Uint32(r.buf[r.pos:])
	r.pos += 4
	return v, nil
}

// ReadFixed64 reads an 8-byte little-endian value.
func (r *Reader) ReadFixed64() (uint64, error) {
	if r.Remaining() < 8 {
		return 0, ErrTruncated
	}
	v := binary.LittleEndian.Uint64(r.buf[r.pos:])
	r.pos += 8
	return v, nil
}

// ReadFloat32 reads a fixed32 and reinterprets it as float32.
func (r *Reader) ReadFloat32() (float32, error) {
	v, err := r.ReadFixed32()
	return math.Float32frombits(v), err
}

// ReadFloat64 reads a fixed64 and reinterprets it as float64.
func (r *Reader) ReadFloat64() (float64, error) {
	v, err := r.ReadFixed64()
	return math.Float64frombits(v), err
}

// ReadBytes reads a length-delimited byte slice. The returned slice aliases
// the underlying buffer. Returns ErrTruncated if the declared length
// exceeds the remaining input.
func (r *Reader) ReadBytes() ([]byte, error) {
	n, err := r.ReadVarint()
	if err != nil {
		return nil, err
	}
	if n > uint64(r.Remaining()) {
		return nil, ErrTruncated
	}
	b := r.buf[r.pos : r.pos+int(n)]
	r.pos += int(n)
	return b, nil
}

// ReadTag reads a field tag and returns the field number and wire type.
func (r *Reader) ReadTag() (int, WireType, error) {
	v, err := r.ReadVarint()
	if err != nil {
		return 0, 0, err
	}
	num := int(v >> 3)
	if num <= 0 {
		return 0, 0, ErrInvalidFieldNumber
	}
	t := WireType(v & 0x7)
	switch t {
	case Varint, Fixed64, Bytes, Fixed32:
		return num, t, nil
	default:
		return 0, 0, fmt.Errorf("%w: %d", ErrInvalidWireType, t)
	}
}

// Skip discards one value of the given wire type, with full bounds checks.
func (r *Reader) Skip(t WireType) error {
	switch t {
	case Varint:
		_, err := r.ReadVarint()
		return err
	case Fixed64:
		_, err := r.ReadFixed64()
		return err
	case Bytes:
		_, err := r.ReadBytes()
		return err
	case Fixed32:
		_, err := r.ReadFixed32()
		return err
	default:
		return fmt.Errorf("%w: %d", ErrInvalidWireType, t)
	}
}

// Field is one decoded field header plus its raw payload view.
type Field struct {
	Number int
	Type   WireType
	// Varint holds the value when Type == Varint.
	Varint uint64
	// Data holds the payload when Type == Bytes (aliases the buffer).
	Data []byte
	// Fixed32 / Fixed64 hold values for the fixed wire types.
	Fixed32 uint32
	Fixed64 uint64
}

// ZigZag decodes Varint as a signed zigzag integer.
func (f Field) ZigZag() int64 { return int64(f.Varint>>1) ^ -int64(f.Varint&1) }

// Next reads the next field from the stream. It returns ok=false when the
// stream is exhausted; a non-nil error indicates malformed or truncated input.
func (r *Reader) Next() (f Field, ok bool, err error) {
	if r.Done() {
		return Field{}, false, nil
	}
	num, typ, err := r.ReadTag()
	if err != nil {
		return Field{}, false, err
	}
	f.Number, f.Type = num, typ
	switch typ {
	case Varint:
		f.Varint, err = r.ReadVarint()
	case Fixed64:
		f.Fixed64, err = r.ReadFixed64()
	case Bytes:
		f.Data, err = r.ReadBytes()
	case Fixed32:
		f.Fixed32, err = r.ReadFixed32()
	}
	if err != nil {
		return Field{}, false, err
	}
	return f, true, nil
}

// PackedVarints decodes a packed repeated varint payload (the Data of a
// Bytes field) into out, returning the extended slice.
func PackedVarints(data []byte, out []uint64) ([]uint64, error) {
	r := NewReader(data)
	for !r.Done() {
		v, err := r.ReadVarint()
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// PackedFixed32 decodes a packed repeated fixed32 payload.
func PackedFixed32(data []byte, out []uint32) ([]uint32, error) {
	if len(data)%4 != 0 {
		return nil, ErrTruncated
	}
	for i := 0; i < len(data); i += 4 {
		out = append(out, binary.LittleEndian.Uint32(data[i:]))
	}
	return out, nil
}

// PackedFixed64 decodes a packed repeated fixed64 payload.
func PackedFixed64(data []byte, out []uint64) ([]uint64, error) {
	if len(data)%8 != 0 {
		return nil, ErrTruncated
	}
	for i := 0; i < len(data); i += 8 {
		out = append(out, binary.LittleEndian.Uint64(data[i:]))
	}
	return out, nil
}

// ---------- Encoder (used by tests, demo, and the sparse optimizer) ----------

// AppendVarint appends v in base-128 varint form.
func AppendVarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// AppendZigZag appends a zigzag-encoded signed integer.
func AppendZigZag(dst []byte, v int64) []byte {
	return AppendVarint(dst, uint64(v<<1)^uint64(v>>63))
}

// AppendTag appends the tag for field num with wire type t.
func AppendTag(dst []byte, num int, t WireType) []byte {
	return AppendVarint(dst, uint64(num)<<3|uint64(t))
}

// AppendFixed32 appends a 4-byte little-endian value.
func AppendFixed32(dst []byte, v uint32) []byte {
	return append(dst, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

// AppendFixed64 appends an 8-byte little-endian value.
func AppendFixed64(dst []byte, v uint64) []byte {
	return append(dst,
		byte(v), byte(v>>8), byte(v>>16), byte(v>>24),
		byte(v>>32), byte(v>>40), byte(v>>48), byte(v>>56))
}

// AppendBytes appends a length-delimited byte slice.
func AppendBytes(dst, b []byte) []byte {
	dst = AppendVarint(dst, uint64(len(b)))
	return append(dst, b...)
}
