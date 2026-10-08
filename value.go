package configeval

import (
	"fmt"
	"strconv"
)

// Kind identifies the dynamic type of a Value.
type Kind int

const (
	KindNum Kind = iota
	KindStr
	KindBool
)

func (k Kind) String() string {
	switch k {
	case KindNum:
		return "number"
	case KindStr:
		return "string"
	case KindBool:
		return "bool"
	}
	return "unknown"
}

// Value is a fully evaluated configuration value.
type Value struct {
	kind    Kind
	num     float64
	str     string
	boolean bool
}

// Num constructs a numeric Value.
func Num(f float64) Value { return Value{kind: KindNum, num: f} }

// Str constructs a string Value.
func Str(s string) Value { return Value{kind: KindStr, str: s} }

// Bool constructs a boolean Value.
func Bool(b bool) Value { return Value{kind: KindBool, boolean: b} }

// Kind reports the dynamic type of the value.
func (v Value) Kind() Kind { return v.kind }

// Num returns the numeric payload; ok is false for non-numbers.
func (v Value) Num() (f float64, ok bool) { return v.num, v.kind == KindNum }

// Str returns the string payload; ok is false for non-strings.
func (v Value) Str() (s string, ok bool) { return v.str, v.kind == KindStr }

// Bool returns the boolean payload; ok is false for non-bools.
func (v Value) Bool() (b bool, ok bool) { return v.boolean, v.kind == KindBool }

// String renders the value in its canonical form.
func (v Value) String() string {
	switch v.kind {
	case KindNum:
		return strconv.FormatFloat(v.num, 'g', -1, 64)
	case KindStr:
		return v.str
	case KindBool:
		return strconv.FormatBool(v.boolean)
	}
	return ""
}

// canonical encodes the value with its type for hashing.
func (v Value) canonical() string {
	switch v.kind {
	case KindNum:
		return "n:" + strconv.FormatFloat(v.num, 'g', -1, 64)
	case KindStr:
		return "s:" + v.str
	case KindBool:
		return "b:" + strconv.FormatBool(v.boolean)
	}
	return "?"
}

// interpolate renders the value for embedding inside a string template.
func (v Value) interpolate() (string, error) {
	switch v.kind {
	case KindStr:
		return v.str, nil
	case KindNum:
		return strconv.FormatFloat(v.num, 'g', -1, 64), nil
	case KindBool:
		return strconv.FormatBool(v.boolean), nil
	}
	return "", fmt.Errorf("cannot interpolate value")
}
