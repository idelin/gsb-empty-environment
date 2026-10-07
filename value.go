// Package schemamig migrates rows between schema versions. Values are
// integers, rationals, text, or null. A conversion between kinds is only
// allowed when a round trip back to the source kind preserves the
// original value exactly.
package schemamig

import (
	"fmt"
	"math/big"
	"strconv"
)

// Kind identifies the type of a Value.
type Kind int

const (
	KindNull Kind = iota
	KindInt
	KindRational
	KindText
	// KindRecord is a nested map of values. It is not a schema field
	// type; it only carries preserved unknown fields under ExtraColumn.
	KindRecord
)

func (k Kind) String() string {
	switch k {
	case KindNull:
		return "null"
	case KindInt:
		return "int"
	case KindRational:
		return "rational"
	case KindText:
		return "text"
	case KindRecord:
		return "record"
	}
	return fmt.Sprintf("kind(%d)", int(k))
}

// Value is a typed field value. Exactly the member matching Kind is set.
type Value struct {
	Kind   Kind
	Int    int64
	Rat    *big.Rat
	Str    string
	Record map[string]Value
}

// Null returns the null value.
func Null() Value { return Value{Kind: KindNull} }

// IntVal returns an integer value.
func IntVal(n int64) Value { return Value{Kind: KindInt, Int: n} }

// RatVal returns a rational value; r is copied.
func RatVal(r *big.Rat) Value {
	return Value{Kind: KindRational, Rat: new(big.Rat).Set(r)}
}

// TextVal returns a text value.
func TextVal(s string) Value { return Value{Kind: KindText, Str: s} }

// RecordVal returns a nested-record value.
func RecordVal(fields map[string]Value) Value {
	return Value{Kind: KindRecord, Record: fields}
}

// Equal reports whether two values are identical in kind and content.
func Equal(a, b Value) bool {
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case KindInt:
		return a.Int == b.Int
	case KindRational:
		return a.Rat.Cmp(b.Rat) == 0
	case KindText:
		return a.Str == b.Str
	case KindRecord:
		if len(a.Record) != len(b.Record) {
			return false
		}
		for k, av := range a.Record {
			bv, ok := b.Record[k]
			if !ok || !Equal(av, bv) {
				return false
			}
		}
		return true
	}
	return true
}

// String renders the value for error messages.
func (v Value) String() string {
	switch v.Kind {
	case KindNull:
		return "null"
	case KindInt:
		return fmt.Sprintf("%d", v.Int)
	case KindRational:
		return v.Rat.RatString()
	case KindText:
		return fmt.Sprintf("%q", v.Str)
	case KindRecord:
		return fmt.Sprintf("%v", v.Record)
	}
	return "?"
}

// Convert converts v to the target kind. The conversion is only allowed
// if converting the result back to the source kind reproduces v exactly;
// otherwise an error is returned. Null is never convertible here; null
// handling lives at the field level.
func Convert(v Value, to Kind) (Value, error) {
	if v.Kind == to {
		return v, nil
	}
	if v.Kind == KindNull || to == KindNull {
		return Value{}, fmt.Errorf("cannot convert %s to %s", v.Kind, to)
	}
	w, err := convertOnce(v, to)
	if err != nil {
		return Value{}, err
	}
	back, err := convertOnce(w, v.Kind)
	if err != nil {
		return Value{}, fmt.Errorf("conversion %s -> %s is not reversible: %v", v.Kind, to, err)
	}
	if !Equal(back, v) {
		return Value{}, fmt.Errorf("conversion %s -> %s loses information: %s becomes %s", v.Kind, to, v, back)
	}
	return w, nil
}

func convertOnce(v Value, to Kind) (Value, error) {
	switch v.Kind {
	case KindInt:
		switch to {
		case KindRational:
			return RatVal(big.NewRat(v.Int, 1)), nil
		case KindText:
			return TextVal(fmt.Sprintf("%d", v.Int)), nil
		}
	case KindRational:
		switch to {
		case KindInt:
			if !v.Rat.IsInt() {
				return Value{}, fmt.Errorf("rational %s is not an integer", v.Rat.RatString())
			}
			n := v.Rat.Num()
			if !n.IsInt64() {
				return Value{}, fmt.Errorf("rational %s overflows int64", v.Rat.RatString())
			}
			return IntVal(n.Int64()), nil
		case KindText:
			return TextVal(v.Rat.RatString()), nil
		}
	case KindText:
		switch to {
		case KindInt:
			n, err := strconv.ParseInt(v.Str, 10, 64)
			if err != nil {
				return Value{}, fmt.Errorf("text %q is not an integer", v.Str)
			}
			return IntVal(n), nil
		case KindRational:
			r, ok := new(big.Rat).SetString(v.Str)
			if !ok {
				return Value{}, fmt.Errorf("text %q is not a rational", v.Str)
			}
			return RatVal(r), nil
		}
	}
	return Value{}, fmt.Errorf("cannot convert %s to %s", v.Kind, to)
}
