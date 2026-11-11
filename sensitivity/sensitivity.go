package sensitivity

import "math/big"

// Sensitivity 描述一个节点输出对单个基础关系中一行增删的最大影响。
//
//   - 标量节点（计数、求和）：Rows 为 nil，Bound 是输出数值的 L1 上界。
//   - 表节点（源、投影、并集、连接）：Bound 是单条输入行可产生的
//     最大输出行数（行级 L1 敏感度），Rows 非 nil 且与 Bound 同值。
//
// Known 为 false 时 Bound 为 nil，Reason 给出无法推导的原因。
type Sensitivity struct {
	Known  bool
	Bound  Cell
	Rows   Cell
	Reason string
}

// Unknown 构造未知敏感度，reason 必须说明缺失的上界或结构性原因。
func Unknown(reason string) Sensitivity {
	return Sensitivity{Known: false, Reason: reason}
}

// ScalarBound 构造已知标量敏感度。
func ScalarBound(bound Cell) Sensitivity {
	return Sensitivity{Known: true, Bound: bound}
}

// RowBound 构造已知行级敏感度（表节点）。
func RowBound(bound Cell) Sensitivity {
	return Sensitivity{Known: true, Bound: bound, Rows: bound}
}

// IsUnknown 报告敏感度是否未知。
func (s Sensitivity) IsUnknown() bool { return !s.Known }

// String 以可读形式渲染敏感度。
func (s Sensitivity) String() string {
	if !s.Known {
		return "未知(" + s.Reason + ")"
	}
	if s.Rows != nil {
		return "行级≤" + s.Rows.RatString()
	}
	return "标量≤" + s.Bound.RatString()
}

func mulRat(a, b Cell) Cell {
	return new(big.Rat).Mul(a, b)
}

func addRat(a, b Cell) Cell {
	return new(big.Rat).Add(a, b)
}
