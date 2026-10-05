package pager

import "strings"

// Kind 标识排序字段值的类型。
type Kind int

const (
	KindString Kind = iota
	KindInt
	KindFloat
	KindBool
)

// Value 是一个可空的排序键值。Null 为 true 时其余字段无意义。
type Value struct {
	Null bool    `json:"n"`
	Kind Kind    `json:"k"`
	S    string  `json:"s,omitempty"`
	I    int64   `json:"i,omitempty"`
	F    float64 `json:"f,omitempty"`
	B    bool    `json:"b,omitempty"`
}

// String 构造字符串值。
func String(s string) Value { return Value{Kind: KindString, S: s} }

// Int 构造整数值。
func Int(i int64) Value { return Value{Kind: KindInt, I: i} }

// Float 构造浮点值。
func Float(f float64) Value { return Value{Kind: KindFloat, F: f} }

// Bool 构造布尔值。
func Bool(b bool) Value { return Value{Kind: KindBool, B: b} }

// Null 构造指定类型的空值。空值之间按类型一致即相等。
func Null(k Kind) Value { return Value{Null: true, Kind: k} }

// compareNonNull 比较两个非空值。类型不同按 Kind 排序，保证确定性。
func compareNonNull(a, b Value) int {
	if a.Kind != b.Kind {
		if a.Kind < b.Kind {
			return -1
		}
		return 1
	}
	switch a.Kind {
	case KindString:
		return strings.Compare(a.S, b.S)
	case KindInt:
		switch {
		case a.I < b.I:
			return -1
		case a.I > b.I:
			return 1
		}
	case KindFloat:
		switch {
		case a.F < b.F:
			return -1
		case a.F > b.F:
			return 1
		}
	case KindBool:
		switch {
		case !a.B && b.B:
			return -1
		case a.B && !b.B:
			return 1
		}
	}
	return 0
}
