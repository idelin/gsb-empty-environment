// Package message parses raw protobuf wire-format buffers into a
// generic, schema-free field tree, and provides unpackers for packed
// repeated fields and nested (length-delimited) messages.
package message

import (
	"fmt"

	"pbufsim/wire"
)

// Field is one decoded (tag, value) pair. Exactly one of the value
// members is meaningful, selected by Type.
type Field struct {
	Num     int       // protobuf field number (>= 1)
	Type    wire.Type // wire type as seen on the wire
	Varint  uint64    // Type == wire.Varint
	Fixed32 uint32    // Type == wire.Fixed32
	Fixed64 uint64    // Type == wire.Fixed64
	Raw     []byte    // Type == wire.Bytes (aliases the input buffer)
	Offset  int       // absolute offset of this field's tag in the buffer
}

// Message is a flat, order-preserving list of decoded fields.
type Message struct {
	Fields []Field
	byNum  map[int][]int // field number -> indexes into Fields
}

// Parse decodes a complete protobuf message from b.
//
// Unknown field numbers are preserved (they are simply Fields with an
// unknown Num). Truncated or malformed input returns an error; the
// parser never reads out of bounds.
func Parse(b []byte) (*Message, error) {
	r := wire.NewReader(b)
	m := &Message{byNum: make(map[int][]int)}
	for !r.Done() {
		off := r.Offset()
		num, typ, err := r.Tag()
		if err != nil {
			return nil, err
		}
		f := Field{Num: num, Type: typ, Offset: off}
		switch typ {
		case wire.Varint:
			f.Varint, err = r.Varint()
		case wire.Fixed64:
			f.Fixed64, err = r.Fixed64()
		case wire.Fixed32:
			f.Fixed32, err = r.Fixed32()
		case wire.Bytes:
			f.Raw, err = r.Bytes()
		}
		if err != nil {
			return nil, fmt.Errorf("message: field %d at offset %d: %w", num, off, err)
		}
		m.byNum[num] = append(m.byNum[num], len(m.Fields))
		m.Fields = append(m.Fields, f)
	}
	return m, nil
}

// Get returns all fields with the given number, in wire order.
// Repeated fields therefore appear as multiple entries.
func (m *Message) Get(num int) []Field {
	idx := m.byNum[num]
	if len(idx) == 0 {
		return nil
	}
	out := make([]Field, len(idx))
	for i, j := range idx {
		out[i] = m.Fields[j]
	}
	return out
}

// Has reports whether the message contains at least one field num.
func (m *Message) Has(num int) bool { return len(m.byNum[num]) > 0 }

// Nested parses f.Raw as an embedded message. It is an error to call
// Nested on a field whose Type is not wire.Bytes.
func Nested(f Field) (*Message, error) {
	if f.Type != wire.Bytes {
		return nil, fmt.Errorf("message: field %d is %s, not an embedded message", f.Num, f.Type)
	}
	sub, err := Parse(f.Raw)
	if err != nil {
		return nil, fmt.Errorf("message: nested field %d: %w", f.Num, err)
	}
	return sub, nil
}

// UnpackVarints decodes a packed repeated varint payload
// (int32/int64/uint32/uint64/bool/enum element families).
func UnpackVarints(b []byte) ([]uint64, error) {
	r := wire.NewReader(b)
	var out []uint64
	for !r.Done() {
		v, err := r.Varint()
		if err != nil {
			return nil, fmt.Errorf("message: packed varints: %w", err)
		}
		out = append(out, v)
	}
	return out, nil
}

// UnpackZigZags decodes a packed repeated sint32/sint64 payload.
func UnpackZigZags(b []byte) ([]int64, error) {
	r := wire.NewReader(b)
	var out []int64
	for !r.Done() {
		v, err := r.ZigZag()
		if err != nil {
			return nil, fmt.Errorf("message: packed zigzags: %w", err)
		}
		out = append(out, v)
	}
	return out, nil
}

// UnpackFixed32s decodes a packed repeated fixed32/float payload.
// The payload length must be a multiple of 4.
func UnpackFixed32s(b []byte) ([]uint32, error) {
	if len(b)%4 != 0 {
		return nil, &wire.TruncatedError{
			What: "packed fixed32", Need: 4 - len(b)%4, Have: 0, Offset: len(b),
		}
	}
	r := wire.NewReader(b)
	out := make([]uint32, 0, len(b)/4)
	for !r.Done() {
		v, err := r.Fixed32()
		if err != nil {
			return nil, fmt.Errorf("message: packed fixed32: %w", err)
		}
		out = append(out, v)
	}
	return out, nil
}

// UnpackFixed64s decodes a packed repeated fixed64/double payload.
// The payload length must be a multiple of 8.
func UnpackFixed64s(b []byte) ([]uint64, error) {
	if len(b)%8 != 0 {
		return nil, &wire.TruncatedError{
			What: "packed fixed64", Need: 8 - len(b)%8, Have: 0, Offset: len(b),
		}
	}
	r := wire.NewReader(b)
	out := make([]uint64, 0, len(b)/8)
	for !r.Done() {
		v, err := r.Fixed64()
		if err != nil {
			return nil, fmt.Errorf("message: packed fixed64: %w", err)
		}
		out = append(out, v)
	}
	return out, nil
}
