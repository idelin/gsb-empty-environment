package canonkey

import (
	"bytes"
	"math"
)

// Kind identifies the concrete type carried by a Value. The kind is part of
// the canonical key encoding, so values of different kinds never collide
// even when their textual forms look identical ("1" vs 1 vs 1.0).
type Kind uint8

const (
	KindNull Kind = iota
	KindString
	KindInt
	KindFloat
	KindBool
	KindBytes
)

// Value is a typed, nullable key field. A Null value is distinct from any
// empty payload: Null() != String("") != Bytes(nil).
type Value struct {
	Kind  Kind
	Str   string
	Int   int64
	Float float64
	Bool  bool
	Blob  []byte
}

// Null returns the explicit null value. It encodes differently from every
// non-null value, including empty strings and empty byte slices.
func Null() Value { return Value{Kind: KindNull} }

// String returns a string-typed field value.
func String(s string) Value { return Value{Kind: KindString, Str: s} }

// Int returns an int64-typed field value.
func Int(i int64) Value { return Value{Kind: KindInt, Int: i} }

// Float returns a float64-typed field value.
func Float(f float64) Value { return Value{Kind: KindFloat, Float: f} }

// Bool returns a bool-typed field value.
func Bool(b bool) Value { return Value{Kind: KindBool, Bool: b} }

// Bytes returns a byte-slice-typed field value. The slice is copied so the
// key stays stable if the caller mutates the original.
func Bytes(b []byte) Value {
	cp := make([]byte, len(b))
	copy(cp, b)
	return Value{Kind: KindBytes, Blob: cp}
}

// Equal reports whether two values are identical in kind and payload.
// Floats are compared by bit pattern so NaN equals NaN and +0 differs
// from -0, matching the canonical encoding exactly.
func (v Value) Equal(o Value) bool {
	if v.Kind != o.Kind {
		return false
	}
	switch v.Kind {
	case KindNull:
		return true
	case KindString:
		return v.Str == o.Str
	case KindInt:
		return v.Int == o.Int
	case KindFloat:
		return math.Float64bits(v.Float) == math.Float64bits(o.Float)
	case KindBool:
		return v.Bool == o.Bool
	case KindBytes:
		return bytes.Equal(v.Blob, o.Blob)
	default:
		return false
	}
}
