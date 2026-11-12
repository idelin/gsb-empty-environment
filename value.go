package netsec

import (
	"encoding/binary"
	"fmt"
)

// Value is the validated result of decoding SSZ bytes. Sub-values share no
// mutable state with the input buffer: raw byte accessors return copies.
type Value struct {
	typ      Type
	raw      []byte
	children []*Value
	selector int
	bits     []bool
}

// Type returns the schema type of the value.
func (v *Value) Type() Type { return v.typ }

// Kind returns the SSZ kind of the value.
func (v *Value) Kind() Kind { return v.typ.Kind() }

// Raw returns a copy of the exact wire bytes that encode this value.
func (v *Value) Raw() []byte {
	out := make([]byte, len(v.raw))
	copy(out, v.raw)
	return out
}

// Len returns the number of elements, fields or bits represented by the value.
func (v *Value) Len() int {
	switch v.typ.Kind() {
	case KindVector, KindList, KindContainer:
		return len(v.children)
	case KindBitvector, KindBitlist:
		return len(v.bits)
	case KindUnion:
		return 1
	default:
		return 1
	}
}

// Uint64 decodes a little-endian basic integer of at most 8 bytes.
func (v *Value) Uint64() (uint64, error) {
	bt, ok := v.typ.(*BasicType)
	if !ok || bt.isBool || bt.size > 8 {
		return 0, fmt.Errorf("%w: Uint64 on %s", ErrTypeMismatch, kindName(v.typ))
	}
	var n uint64
	for i, b := range v.raw {
		n |= uint64(b) << (8 * i)
	}
	return n, nil
}

// Bool returns a decoded boolean value.
func (v *Value) Bool() (bool, error) {
	bt, ok := v.typ.(*BasicType)
	if !ok || !bt.isBool {
		return false, fmt.Errorf("%w: Bool on %s", ErrTypeMismatch, kindName(v.typ))
	}
	return v.raw[0] == 1, nil
}

// Bytes returns a copy of a basic value's raw bytes (e.g. a uint256).
func (v *Value) Bytes() ([]byte, error) {
	if _, ok := v.typ.(*BasicType); !ok {
		return nil, fmt.Errorf("%w: Bytes on %s", ErrTypeMismatch, kindName(v.typ))
	}
	return v.Raw(), nil
}

// Elements returns vector or list member values in order.
func (v *Value) Elements() ([]*Value, error) {
	switch v.typ.Kind() {
	case KindVector, KindList:
		return append([]*Value(nil), v.children...), nil
	default:
		return nil, fmt.Errorf("%w: Elements on %s", ErrTypeMismatch, kindName(v.typ))
	}
}

// Fields returns container field values in schema order.
func (v *Value) Fields() ([]*Value, error) {
	if v.typ.Kind() != KindContainer {
		return nil, fmt.Errorf("%w: Fields on %s", ErrTypeMismatch, kindName(v.typ))
	}
	return append([]*Value(nil), v.children...), nil
}

// FieldByName returns the named container field.
func (v *Value) FieldByName(name string) (*Value, error) {
	ct, ok := v.typ.(*ContainerType)
	if !ok {
		return nil, fmt.Errorf("%w: FieldByName on %s", ErrTypeMismatch, kindName(v.typ))
	}
	idx, ok := ct.FieldIndex(name)
	if !ok {
		return nil, fmt.Errorf("ssz: unknown field %q", name)
	}
	return v.children[idx], nil
}

// Selector returns the union selector that was present on the wire.
func (v *Value) Selector() (int, error) {
	if v.typ.Kind() != KindUnion {
		return 0, fmt.Errorf("%w: Selector on %s", ErrTypeMismatch, kindName(v.typ))
	}
	return v.selector, nil
}

// Option returns the decoded union member value.
func (v *Value) Option() (*Value, error) {
	if v.typ.Kind() != KindUnion {
		return nil, fmt.Errorf("%w: Option on %s", ErrTypeMismatch, kindName(v.typ))
	}
	return v.children[0], nil
}

// Bits returns bitvector bits, or bitlist bits with the delimiter removed.
func (v *Value) Bits() ([]bool, error) {
	switch v.typ.Kind() {
	case KindBitvector, KindBitlist:
		return append([]bool(nil), v.bits...), nil
	default:
		return nil, fmt.Errorf("%w: Bits on %s", ErrTypeMismatch, kindName(v.typ))
	}
}

func kindName(t Type) string {
	switch t.Kind() {
	case KindBasic:
		return "basic"
	case KindVector:
		return "vector"
	case KindList:
		return "list"
	case KindBitvector:
		return "bitvector"
	case KindBitlist:
		return "bitlist"
	case KindContainer:
		return "container"
	case KindUnion:
		return "union"
	default:
		return "unknown"
	}
}

func putUint32(b []byte, v uint32) {
	binary.LittleEndian.PutUint32(b, v)
}

func readUint32(b []byte) uint32 {
	return binary.LittleEndian.Uint32(b)
}
