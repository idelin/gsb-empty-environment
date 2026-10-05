package sccp

import "strconv"

// Kind 是常量传播格上元素的种类。
// 格的偏序关系为：Undef(⊥) < Const < Over(⊤)。
type Kind int

const (
	// Undef 表示 ⊥：值尚未确定（对应的路径尚未被分析到达）。
	// 注意：Undef 绝不意味着"永不可达"，只表示"目前还不知道"。
	Undef Kind = iota
	// Const 表示已确定的 8 位常量。
	Const
	// Over 表示 ⊤：值被过度定义（多个不同常量合流，或来自未知输入）。
	Over
)

// Value 是格上的一个元素：一个 8 位值的抽象。
type Value struct {
	Kind Kind
	Val  uint8 // 仅当 Kind == Const 时有效
}

// Undefined 返回 ⊥。
func Undefined() Value { return Value{Kind: Undef} }

// Constant 返回常量格元素。
func Constant(v uint8) Value { return Value{Kind: Const, Val: v} }

// Overdefined 返回 ⊤。
func Overdefined() Value { return Value{Kind: Over} }

// Meet 计算两个格元素的最小上界（合流/join）。
// 该运算保证单调：Meet(a, b) ≥ a 且 Meet(a, b) ≥ b。
func Meet(a, b Value) Value {
	if a.Kind == Undef {
		return b
	}
	if b.Kind == Undef {
		return a
	}
	if a.Kind == Over || b.Kind == Over {
		return Overdefined()
	}
	if a.Val == b.Val {
		return a
	}
	return Overdefined()
}

// Leq 报告 a ≤ b 是否在格序上成立。
func Leq(a, b Value) bool { return Meet(a, b) == b }

func (v Value) String() string {
	switch v.Kind {
	case Undef:
		return "⊥"
	case Over:
		return "⊤"
	default:
		return strconv.Itoa(int(v.Val))
	}
}
