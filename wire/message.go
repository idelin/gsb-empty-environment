package wire

import (
	"fmt"
	"sort"
)

// Value is one raw occurrence of a field on the wire.
type Value struct {
	Wire  WireType
	U64   uint64 // valid for WireVarint, WireFixed32 (low 32 bits), WireFixed64
	Bytes []byte // valid for WireLengthDelimited (aliases input buffer)
}

// Message is a parsed protobuf message: field number -> ordered occurrences.
type Message map[int][]Value

// Parse decodes a whole buffer into a Message. Unknown fields are kept raw.
// Any truncation, overflow or bad wire type aborts with an error; the
// decoder never reads out of bounds.
func Parse(buf []byte) (Message, error) {
	d := NewDecoder(buf)
	m := Message{}
	for !d.EOF() {
		num, wt, err := d.ReadTag()
		if err != nil {
			return nil, fmt.Errorf("wire: offset %d: %v", d.Pos(), err)
		}
		v := Value{Wire: wt}
		switch wt {
		case WireVarint:
			if v.U64, err = d.ReadVarint(); err != nil {
				return nil, fmt.Errorf("wire: field %d varint: %v", num, err)
			}
		case WireFixed64:
			if v.U64, err = d.ReadFixed64(); err != nil {
				return nil, fmt.Errorf("wire: field %d fixed64: %v", num, err)
			}
		case WireFixed32:
			u32, err := d.ReadFixed32()
			if err != nil {
				return nil, fmt.Errorf("wire: field %d fixed32: %v", num, err)
			}
			v.U64 = uint64(u32)
		case WireLengthDelimited:
			if v.Bytes, err = d.ReadBytes(); err != nil {
				return nil, fmt.Errorf("wire: field %d bytes: %v", num, err)
			}
		default:
			return nil, fmt.Errorf("wire: field %d: %v", num, ErrBadWireType)
		}
		m[num] = append(m[num], v)
	}
	return m, nil
}

// FieldNumbers returns the sorted field numbers present in the message.
func (m Message) FieldNumbers() []int {
	out := make([]int, 0, len(m))
	for n := range m {
		out = append(out, n)
	}
	sort.Ints(out)
	return out
}

// Encoder builds protobuf binary buffers. It is the mirror of Decoder and
// exists so tests and demos can construct streams without external deps.
type Encoder struct {
	buf []byte
}

// NewEncoder returns an empty Encoder.
func NewEncoder() *Encoder { return &Encoder{} }

// Bytes returns the accumulated buffer.
func (e *Encoder) Bytes() []byte { return e.buf }

// AppendVarint appends v in base-128 varint form.
func (e *Encoder) AppendVarint(v uint64) {
	for v >= 0x80 {
		e.buf = append(e.buf, byte(v)|0x80)
		v >>= 7
	}
	e.buf = append(e.buf, byte(v))
}

// AppendTag appends the tag for field num with wire type wt.
func (e *Encoder) AppendTag(num int, wt WireType) {
	e.AppendVarint(uint64(num)<<3 | uint64(wt))
}

// AppendFixed32 appends 4 little-endian bytes.
func (e *Encoder) AppendFixed32(v uint32) {
	e.buf = append(e.buf, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

// AppendFixed64 appends 8 little-endian bytes.
func (e *Encoder) AppendFixed64(v uint64) {
	e.buf = append(e.buf, byte(v), byte(v>>8), byte(v>>16), byte(v>>24),
		byte(v>>32), byte(v>>40), byte(v>>48), byte(v>>56))
}

// AppendBytes appends a length prefix plus raw bytes.
func (e *Encoder) AppendBytes(b []byte) {
	e.AppendVarint(uint64(len(b)))
	e.buf = append(e.buf, b...)
}

// EncodeZigZag maps a signed int64 to its ZigZag uint64 form.
func EncodeZigZag(v int64) uint64 { return uint64(v<<1) ^ uint64(v>>63) }

// AddVarintField appends a full tagged varint field.
func (e *Encoder) AddVarintField(num int, v uint64) {
	e.AppendTag(num, WireVarint)
	e.AppendVarint(v)
}

// AddZigZagField appends a tagged ZigZag-encoded signed field.
func (e *Encoder) AddZigZagField(num int, v int64) {
	e.AddVarintField(num, EncodeZigZag(v))
}

// AddFixed32Field appends a tagged fixed32 field.
func (e *Encoder) AddFixed32Field(num int, v uint32) {
	e.AppendTag(num, WireFixed32)
	e.AppendFixed32(v)
}

// AddFixed64Field appends a tagged fixed64 field.
func (e *Encoder) AddFixed64Field(num int, v uint64) {
	e.AppendTag(num, WireFixed64)
	e.AppendFixed64(v)
}

// AddBytesField appends a tagged length-delimited field (string/bytes/message).
func (e *Encoder) AddBytesField(num int, b []byte) {
	e.AppendTag(num, WireLengthDelimited)
	e.AppendBytes(b)
}
