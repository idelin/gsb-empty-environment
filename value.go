// Package migrate converts rows between schema versions. A conversion is
// allowed by default only when it round-trips: converting a value to the
// target type and back must reproduce the original value exactly.
package migrate

import (
	"fmt"
	"math/big"
	"strconv"
)

// Kind is the runtime type of a Value.
type Kind int

const (
	Null Kind = iota
	Int
	Rat
	Text
)

func (k Kind) String() string {
	switch k {
	case Null:
		return "null"
	case Int:
		return "int"
	case Rat:
		return "rational"
	case Text:
		return "text"
	}
	return "unknown"
}

// Value is a typed field value. The zero Value is Null.
type Value struct {
	kind Kind
	i    int64
	r    *big.Rat
	s    string
}

// IntVal builds an integer Value.
func IntVal(i int64) Value { return Value{kind: Int, i: i} }

// RatVal builds a rational Value, copying r.
func RatVal(r *big.Rat) Value { return Value{kind: Rat, r: new(big.Rat).Set(r)} }

// TextVal builds a text Value.
func TextVal(s string) Value { return Value{kind: Text, s: s} }

// NullVal builds the null Value.
func NullVal() Value { return Value{kind: Null} }

// Kind reports the runtime type of v.
func (v Value) Kind() Kind { return v.kind }

// IsNull reports whether v is null.
func (v Value) IsNull() bool { return v.kind == Null }

// Int returns the integer payload; it panics if v is not an Int.
func (v Value) Int() int64 {
	if v.kind != Int {
		panic("migrate: Int called on " + v.kind.String())
	}
	return v.i
}

// Rat returns a copy of the rational payload; it panics if v is not a Rat.
func (v Value) Rat() *big.Rat {
	if v.kind != Rat {
		panic("migrate: Rat called on " + v.kind.String())
	}
	return new(big.Rat).Set(v.r)
}

// Text returns the text payload; it panics if v is not a Text.
func (v Value) Text() string {
	if v.kind != Text {
		panic("migrate: Text called on " + v.kind.String())
	}
	return v.s
}

// Equal reports whether v and o are the same kind and carry equal payloads.
func (v Value) Equal(o Value) bool {
	if v.kind != o.kind {
		return false
	}
	switch v.kind {
	case Int:
		return v.i == o.i
	case Rat:
		return v.r.Cmp(o.r) == 0
	case Text:
		return v.s == o.s
	}
	return true
}

func (v Value) String() string {
	switch v.kind {
	case Null:
		return "null"
	case Int:
		return strconv.FormatInt(v.i, 10)
	case Rat:
		return ratText(v.r)
	case Text:
		return strconv.Quote(v.s)
	}
	return fmt.Sprintf("Value(%d)", int(v.kind))
}

// ratText is the canonical text form of a rational: an integer when the
// denominator is 1, otherwise "num/den" in lowest terms.
func ratText(r *big.Rat) string {
	if r.IsInt() {
		return r.Num().String()
	}
	return r.String()
}
