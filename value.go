package parquetcol

import (
	"fmt"
	"math"
)

// Value is the in-memory representation of a record field. Nested records are
// represented with Struct, lists with List, and missing values with the zero
// Value. An empty struct (Struct with no entries) and an empty list (List with
// no entries) are first-class values and are preserved on round trip.
type Value struct {
	kind  valueKind
	i64   int64
	u64   uint64
	f64   float64
	bs    []byte
	str   string
	list  []Value
	stct  map[string]Value
	valid bool
}

type valueKind uint8

const (
	kindNull valueKind = iota
	kindBool
	kindInt32
	kindInt64
	kindFloat
	kindDouble
	kindBytes
	kindString
	kindList
	kindStruct
)

// Null returns a missing value.
func Null() Value { return Value{} }

// IsNull reports whether v is missing.
func (v Value) IsNull() bool { return !v.valid || v.kind == kindNull }

// Bool returns a BOOLEAN value.
func Bool(b bool) Value {
	v := Value{kind: kindBool, valid: true}
	if b {
		v.u64 = 1
	}
	return v
}

// Int32 returns an INT32 value.
func Int32(n int32) Value { return Value{kind: kindInt32, i64: int64(n), valid: true} }

// Int64 returns an INT64 value.
func Int64(n int64) Value { return Value{kind: kindInt64, i64: n, valid: true} }

// Float32 returns a FLOAT value.
func Float32(f float32) Value { return Value{kind: kindFloat, f64: float64(f), valid: true} }

// Float64 returns a DOUBLE value.
func Float64(f float64) Value { return Value{kind: kindDouble, f64: f, valid: true} }

// Bytes returns a BYTE_ARRAY value.
func Bytes(b []byte) Value {
	cp := append([]byte(nil), b...)
	return Value{kind: kindBytes, bs: cp, valid: true}
}

// String returns a UTF8 BYTE_ARRAY value.
func String(s string) Value { return Value{kind: kindString, str: s, valid: true} }

// List returns a list value. An empty (non-nil) slice represents an empty list.
func List(items []Value) Value {
	cp := append([]Value(nil), items...)
	return Value{kind: kindList, list: cp, valid: true}
}

// Struct returns a record/group value. An empty non-nil map represents an
// empty (zero-field) struct; keys correspond to node names in the schema.
func Struct(fields map[string]Value) Value {
	cp := make(map[string]Value, len(fields))
	for k, val := range fields {
		cp[k] = val
	}
	return Value{kind: kindStruct, stct: cp, valid: true}
}

// Kind reports the value type.
func (v Value) Kind() ValueKind { return ValueKind(v.kind) }

// ValueKind enumerates the in-memory value types.
type ValueKind int

// Value kind constants.
const (
	KindNull ValueKind = iota
	KindBool
	KindInt32
	KindInt64
	KindFloat
	KindDouble
	KindBytes
	KindString
	KindList
	KindStruct
)

// BoolValue extracts a boolean.
func (v Value) BoolValue() bool { return v.u64 != 0 }

// Int32Value extracts an int32.
func (v Value) Int32Value() int32 { return int32(v.i64) }

// Int64Value extracts an int64.
func (v Value) Int64Value() int64 { return v.i64 }

// Float32Value extracts a float32.
func (v Value) Float32Value() float32 { return float32(v.f64) }

// Float64Value extracts a float64.
func (v Value) Float64Value() float64 { return v.f64 }

// BytesValue extracts a byte array.
func (v Value) BytesValue() []byte { return v.bs }

// StringValue extracts a string.
func (v Value) StringValue() string {
	if v.kind == kindString {
		return v.str
	}
	return string(v.bs)
}

// ListValue extracts the list elements.
func (v Value) ListValue() []Value { return v.list }

// StructValue extracts the struct fields.
func (v Value) StructValue() map[string]Value { return v.stct }

// Equal reports whether two values are deeply equal. Missing values, empty
// lists and empty structs compare equal to their counterparts.
func (v Value) Equal(o Value) bool {
	if v.IsNull() || o.IsNull() {
		return v.IsNull() == o.IsNull()
	}
	if v.kind != o.kind {
		return false
	}
	switch v.kind {
	case kindBool:
		return (v.u64 != 0) == (o.u64 != 0)
	case kindInt32, kindInt64:
		return v.i64 == o.i64
	case kindFloat:
		return math.Float32bits(v.Float32Value()) == math.Float32bits(o.Float32Value())
	case kindDouble:
		return math.Float64bits(v.f64) == math.Float64bits(o.f64)
	case kindBytes:
		return string(v.bs) == string(o.bs)
	case kindString:
		return v.str == o.str
	case kindList:
		if len(v.list) != len(o.list) {
			return false
		}
		for i := range v.list {
			if !v.list[i].Equal(o.list[i]) {
				return false
			}
		}
		return true
	case kindStruct:
		for k, fv := range v.stct {
			ov, ok := o.stct[k]
			if !ok {
				if !fv.IsNull() {
					return false
				}
				continue
			}
			if !fv.Equal(ov) {
				return false
			}
		}
		for k, ov := range o.stct {
			if _, ok := v.stct[k]; !ok && !ov.IsNull() {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// GoString gives a deterministic debug rendering.
func (v Value) GoString() string {
	switch {
	case v.IsNull():
		return "null"
	case v.kind == kindBool:
		return fmt.Sprintf("bool(%v)", v.u64 != 0)
	case v.kind == kindInt32:
		return fmt.Sprintf("int32(%d)", v.Int32Value())
	case v.kind == kindInt64:
		return fmt.Sprintf("int64(%d)", v.i64)
	case v.kind == kindFloat:
		return fmt.Sprintf("float(%g)", v.Float32Value())
	case v.kind == kindDouble:
		return fmt.Sprintf("double(%g)", v.f64)
	case v.kind == kindBytes:
		return fmt.Sprintf("bytes(%q)", string(v.bs))
	case v.kind == kindString:
		return fmt.Sprintf("string(%q)", v.str)
	case v.kind == kindList:
		s := "list["
		for i, it := range v.list {
			if i > 0 {
				s += ", "
			}
			s += it.GoString()
		}
		return s + "]"
	case v.kind == kindStruct:
		s := "struct{"
		first := true
		for k, fv := range v.stct {
			if !first {
				s += ", "
			}
			first = false
			s += k + "=" + fv.GoString()
		}
		return s + "}"
	default:
		return "unknown"
	}
}
