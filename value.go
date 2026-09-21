package bridge

import (
	"bytes"
	"fmt"
)

// Value is a typed field value.
type Value struct {
	Type  FieldType
	Int   int64
	Bool  bool
	Str   string
	Bytes []byte
}

func IntVal(v int64) Value    { return Value{Type: TyInt, Int: v} }
func BoolVal(v bool) Value    { return Value{Type: TyBool, Bool: v} }
func StrVal(v string) Value   { return Value{Type: TyString, Str: v} }
func BytesVal(v []byte) Value { return Value{Type: TyBytes, Bytes: v} }

// Equal reports whether two values are identical in type and content.
func (v Value) Equal(o Value) bool {
	if v.Type != o.Type {
		return false
	}
	switch v.Type {
	case TyInt:
		return v.Int == o.Int
	case TyBool:
		return v.Bool == o.Bool
	case TyString:
		return v.Str == o.Str
	case TyBytes:
		return bytes.Equal(v.Bytes, o.Bytes)
	}
	return false
}

func (v Value) String() string {
	switch v.Type {
	case TyInt:
		return fmt.Sprintf("%d", v.Int)
	case TyBool:
		return fmt.Sprintf("%v", v.Bool)
	case TyString:
		return fmt.Sprintf("%q", v.Str)
	case TyBytes:
		return fmt.Sprintf("%x", v.Bytes)
	}
	return "?"
}
