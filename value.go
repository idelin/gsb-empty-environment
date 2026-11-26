package mvrewrite

import (
	"cmp"
	"fmt"
	"strings"
)

// Value is the package-internal SQL-style value representation.
//
// Null is represented explicitly.  A padded outer-join row and a base-table
// NULL both become Null; provenance is intentionally not retained by the
// relational evaluator because an optimizer compensation expression can only
// observe exported view columns.
type Value struct {
	Null bool
	I    int64
	S    string
	kind valueKind
}

type valueKind byte

const (
	kindNull valueKind = iota
	kindInt
	kindString
)

// Int returns a non-NULL integer value.
func Int(v int64) Value { return Value{kind: kindInt, I: v} }

// String returns a non-NULL string value.
func String(v string) Value { return Value{kind: kindString, S: v} }

// NullValue returns SQL NULL.
func NullValue() Value { return Value{Null: true, kind: kindNull} }

// NormalizeValue converts common Go values to Value.  nil is SQL NULL.
func NormalizeValue(v any) (Value, error) {
	switch x := v.(type) {
	case nil:
		return NullValue(), nil
	case Value:
		if x.Null {
			return NullValue(), nil
		}
		return x, nil
	case int:
		return Int(int64(x)), nil
	case int8:
		return Int(int64(x)), nil
	case int16:
		return Int(int64(x)), nil
	case int32:
		return Int(int64(x)), nil
	case int64:
		return Int(x), nil
	case uint:
		return Int(int64(x)), nil
	case uint64:
		if x > 1<<63-1 {
			return Value{}, fmt.Errorf("mvrewrite: unsigned value %d overflows int64", x)
		}
		return Int(int64(x)), nil
	case float64:
		return Int(int64(x)), nil
	case float32:
		return Int(int64(x)), nil
	case string:
		return String(x), nil
	case bool:
		if x {
			return Int(1), nil
		}
		return Int(0), nil
	default:
		return Value{}, fmt.Errorf("mvrewrite: unsupported value type %T", v)
	}
}

func mustNormalize(v any) Value {
	out, err := NormalizeValue(v)
	if err != nil {
		panic(err)
	}
	return out
}

// CompareValues compares two non-NULL values.  It returns an error for NULL or
// incomparable types.
func CompareValues(a, b Value) (int, error) {
	if a.Null || b.Null {
		return 0, fmt.Errorf("NULL is not comparable")
	}
	if a.kind != b.kind {
		return 0, fmt.Errorf("incomparable value kinds %d and %d", a.kind, b.kind)
	}
	switch a.kind {
	case kindInt:
		return cmp.Compare(a.I, b.I), nil
	case kindString:
		return strings.Compare(a.S, b.S), nil
	default:
		return 0, fmt.Errorf("cannot compare kind %d", a.kind)
	}
}

// ValuesEqual is NULL-aware only in the sense that NULL never satisfies an
// equi-join/comparison predicate.  Callers evaluating IS [NOT] NULL must handle
// NULL separately.
func ValuesEqual(a, b Value) bool {
	if a.Null || b.Null || a.kind != b.kind {
		return false
	}
	switch a.kind {
	case kindInt:
		return a.I == b.I
	case kindString:
		return a.S == b.S
	default:
		return false
	}
}

// Key renders a stable set-membership key.  The "N:" prefix is essential: a
// padded NULL and the integer/string zero value must not collapse.
func (v Value) Key() string {
	if v.Null {
		return "N"
	}
	switch v.kind {
	case kindInt:
		return fmt.Sprintf("I:%d", v.I)
	case kindString:
		return "S:" + v.S
	default:
		return "?"
	}
}

// Any returns the value as a conventional Go any.  NULL is nil.
func (v Value) Any() any {
	if v.Null {
		return nil
	}
	if v.kind == kindString {
		return v.S
	}
	return v.I
}

func (v Value) String() string {
	if v.Null {
		return "NULL"
	}
	if v.kind == kindString {
		return fmt.Sprintf("%q", v.S)
	}
	return fmt.Sprint(v.I)
}
