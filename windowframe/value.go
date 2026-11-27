// Package windowframe evaluates SQL:2011 window specifications, including the
// ROWS, RANGE and GROUPS frame modes, all four frame exclusion clauses and the
// standard aggregate, value and ranking window functions.
package windowframe

import (
	"errors"
	"fmt"
)

// Kind identifies the concrete type carried by a Value.
type Kind uint8

const (
	KindNull Kind = iota
	KindInt
	KindFloat
	KindBool
	KindString
)

// Value is a single scalar cell of an input row. The zero Value is SQL NULL.
type Value struct {
	kind Kind
	i    int64
	f    float64
	b    bool
	s    string
}

// Null returns the NULL value.
func Null() Value { return Value{kind: KindNull} }

// Int returns an integer value.
func Int(v int64) Value { return Value{kind: KindInt, i: v} }

// Float returns a floating point value. NaN and Inf are not supported.
func Float(v float64) Value { return Value{kind: KindFloat, f: v} }

// Bool returns a boolean value.
func Bool(v bool) Value { return Value{kind: KindBool, b: v} }

// String returns a string value.
func String(v string) Value { return Value{kind: KindString, s: v} }

// Kind reports the type tag of the value.
func (v Value) Kind() Kind { return v.kind }

// IsNull reports whether the value is NULL.
func (v Value) IsNull() bool { return v.kind == KindNull }

// AsInt returns the integer payload.
func (v Value) AsInt() int64 { return v.i }

// AsFloat returns the floating point payload.
func (v Value) AsFloat() float64 { return v.f }

// AsBool returns the boolean payload.
func (v Value) AsBool() bool { return v.b }

// AsString returns the string payload.
func (v Value) AsString() string { return v.s }

// AsNumber returns the numeric payload as float64 together with true for
// integer and floating point values.
func (v Value) AsNumber() (float64, bool) {
	switch v.kind {
	case KindInt:
		return float64(v.i), true
	case KindFloat:
		return v.f, true
	default:
		return 0, false
	}
}

// Equal reports whether two values are the same typed value. NULLs compare
// equal, which is the semantics used for PARTITION BY grouping and peer
// detection rather than ordinary SQL comparison.
func Equal(a, b Value) bool {
	if a.kind != b.kind {
		return false
	}
	switch a.kind {
	case KindNull:
		return true
	case KindInt:
		return a.i == b.i
	case KindFloat:
		return a.f == b.f
	case KindBool:
		return a.b == b.b
	default:
		return a.s == b.s
	}
}

// CmpResult is the outcome of Compare.
type CmpResult int

const (
	CmpLT CmpResult = -1
	CmpEQ CmpResult = 0
	CmpGT CmpResult = 1
)

// Compare orders two non-null values in ascending key order. Integers and
// floating point numbers compare numerically against each other. Booleans and
// strings only compare within their own type. NULL has no relative order and
// is reported as an error.
func Compare(a, b Value) (CmpResult, error) {
	if a.kind == KindNull || b.kind == KindNull {
		return 0, errors.New("windowframe: NULL values have no sort comparison")
	}
	an, anum := a.AsNumber()
	bn, bnum := b.AsNumber()
	if anum && bnum {
		switch {
		case an < bn:
			return CmpLT, nil
		case an > bn:
			return CmpGT, nil
		default:
			return CmpEQ, nil
		}
	}
	if a.kind != b.kind {
		return 0, fmt.Errorf("windowframe: cannot compare %s with %s", a.kind, b.kind)
	}
	switch a.kind {
	case KindBool:
		switch {
		case a.b == b.b:
			return CmpEQ, nil
		case !a.b && b.b:
			return CmpLT, nil
		default:
			return CmpGT, nil
		}
	case KindString:
		switch {
		case a.s < b.s:
			return CmpLT, nil
		case a.s > b.s:
			return CmpGT, nil
		default:
			return CmpEQ, nil
		}
	}
	return 0, fmt.Errorf("windowframe: cannot compare %s with %s", a.kind, b.kind)
}

func (k Kind) String() string {
	switch k {
	case KindNull:
		return "NULL"
	case KindInt:
		return "INTEGER"
	case KindFloat:
		return "FLOAT"
	case KindBool:
		return "BOOLEAN"
	case KindString:
		return "STRING"
	default:
		return "UNKNOWN"
	}
}
