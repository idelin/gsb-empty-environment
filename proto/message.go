// Package proto parses raw Protocol Buffers binary streams into generic
// field lists, with support for nested length-delimited messages and
// packed repeated arrays. Parsing is fully hand-rolled and bounds-safe.
package proto

import (
	"errors"
	"fmt"

	"protowire/wire"
)

// Wire types as defined by the Protocol Buffers binary encoding.
const (
	WireVarint          = 0
	WireFixed64         = 1
	WireLengthDelimited = 2
	WireStartGroup      = 3
	WireEndGroup        = 4
	WireFixed32         = 5
)

// ErrBadWireType is returned for reserved/invalid wire types or a tag of 0.
var ErrBadWireType = errors.New("proto: invalid wire type or field number")

// MaxDepth bounds nested-message recursion so adversarial input cannot
// exhaust the stack.
const MaxDepth = 100

// Field is one decoded key/value pair of a message.
type Field struct {
	Num      int    // field number from the tag
	WireType int    // one of the Wire* constants
	U64      uint64 // value for WireVarint / WireFixed32 / WireFixed64
	Bytes    []byte // value for WireLengthDelimited (aliases the input)
}

// AsZigZag64 interprets a varint field as sint64.
func (f Field) AsZigZag64() int64 { return wire.ZigZagDecode64(f.U64) }

// AsZigZag32 interprets a varint field as sint32.
func (f Field) AsZigZag32() int32 { return wire.ZigZagDecode32(uint32(f.U64)) }

// Message is a parsed sequence of fields in wire order.
type Message []Field

// Parse decodes one message from b. Equivalent to ParseDepth(b, 0).
func Parse(b []byte) (Message, error) { return ParseDepth(b, 0) }

// ParseDepth decodes one message, tracking nesting depth for recursion
// protection. Every field is bounds-checked; truncation is reported via
// wire.ErrTruncated / *wire.TruncatedError.
func ParseDepth(b []byte, depth int) (Message, error) {
	if depth > MaxDepth {
		return nil, fmt.Errorf("proto: nesting depth exceeds %d", MaxDepth)
	}
	r := wire.NewReader(b)
	var msg Message
	for !r.Done() {
		tag, err := r.ReadVarint()
		if err != nil {
			return nil, fmt.Errorf("proto: tag at offset %d: %v", r.Pos(), err)
		}
		num := int(tag >> 3)
		wt := int(tag & 0x7)
		if num <= 0 {
			return nil, fmt.Errorf("proto: field number %d: %v", num, ErrBadWireType)
		}
		f := Field{Num: num, WireType: wt}
		switch wt {
		case WireVarint:
			v, err := r.ReadVarint()
			if err != nil {
				return nil, fmt.Errorf("proto: field %d varint: %v", num, err)
			}
			f.U64 = v
		case WireFixed64:
			v, err := r.ReadFixed64()
			if err != nil {
				return nil, fmt.Errorf("proto: field %d fixed64: %v", num, err)
			}
			f.U64 = v
		case WireLengthDelimited:
			data, err := r.ReadLengthDelimited()
			if err != nil {
				return nil, fmt.Errorf("proto: field %d bytes: %v", num, err)
			}
			f.Bytes = data
		case WireFixed32:
			v, err := r.ReadFixed32()
			if err != nil {
				return nil, fmt.Errorf("proto: field %d fixed32: %v", num, err)
			}
			f.U64 = uint64(v)
		default:
			// Groups (3/4) are deprecated and unsupported; 6/7 are reserved.
			return nil, fmt.Errorf("proto: field %d wire type %d: %v", num, wt, ErrBadWireType)
		}
		msg = append(msg, f)
	}
	return msg, nil
}

// ParseNested parses f.Bytes as a sub-message (for WireLengthDelimited
// fields that carry an embedded message).
func (f Field) ParseNested(depth int) (Message, error) {
	if f.WireType != WireLengthDelimited {
		return nil, fmt.Errorf("proto: field %d is not length-delimited", f.Num)
	}
	return ParseDepth(f.Bytes, depth+1)
}

// ByNum returns all fields with the given field number, in wire order.
func (m Message) ByNum(num int) []Field {
	var out []Field
	for _, f := range m {
		if f.Num == num {
			out = append(out, f)
		}
	}
	return out
}

// PackedVarints decodes a packed repeated varint payload.
func PackedVarints(b []byte) ([]uint64, error) {
	r := wire.NewReader(b)
	var out []uint64
	for !r.Done() {
		v, err := r.ReadVarint()
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// PackedZigZag64 decodes a packed repeated sint64 payload.
func PackedZigZag64(b []byte) ([]int64, error) {
	us, err := PackedVarints(b)
	if err != nil {
		return nil, err
	}
	out := make([]int64, len(us))
	for i, u := range us {
		out[i] = wire.ZigZagDecode64(u)
	}
	return out, nil
}

// PackedFixed32 decodes a packed repeated fixed32 payload; the length must
// be an exact multiple of 4.
func PackedFixed32(b []byte) ([]uint32, error) {
	if len(b)%4 != 0 {
		return nil, fmt.Errorf("proto: packed fixed32 length %d not multiple of 4: %v",
			len(b), wire.ErrTruncated)
	}
	r := wire.NewReader(b)
	out := make([]uint32, 0, len(b)/4)
	for !r.Done() {
		v, err := r.ReadFixed32()
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// PackedFixed64 decodes a packed repeated fixed64 payload; the length must
// be an exact multiple of 8.
func PackedFixed64(b []byte) ([]uint64, error) {
	if len(b)%8 != 0 {
		return nil, fmt.Errorf("proto: packed fixed64 length %d not multiple of 8: %v",
			len(b), wire.ErrTruncated)
	}
	r := wire.NewReader(b)
	out := make([]uint64, 0, len(b)/8)
	for !r.Done() {
		v, err := r.ReadFixed64()
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// EncodeTag returns the varint tag for a field number and wire type.
func EncodeTag(num, wireType int) uint64 {
	return uint64(num)<<3 | uint64(wireType)
}
