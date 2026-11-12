// Package netsec 提供 printf 风格格式串的静态消费分析，供日志安全审查宿主调用。
//
// 分析器逐个转换说明符模拟 Go 标准库 fmt 的参数消费过程，记录每一次参数
// 消费的来源位置与类型约束，重点覆盖：
//   - 显式位置（%[n]verb、%[n]*）与顺序位置（%verb、%*）两种模式，二者禁止混用；
//   - 动态宽度 * 与动态精度 .* 均独立占用一个整数参数；
//   - 整数与字符串等转换各自携带类型约束，同一位置出现冲突约束即上报；
//   - 受污（不可信）格式串无法静态保证，结果的 Guaranteed 为 false。
package netsec

// Kind 描述格式串对单个参数的类型约束。
type Kind int

const (
	// KindUnknown 表示无法归入下列类别的约束。
	KindUnknown Kind = iota
	// KindInt 整数类约束：整型宽度/精度参数，以及 b/c/d/o/O/u/x/X 等转换。
	KindInt
	// KindString 字符串类约束：s、q 转换。
	KindString
	// KindFloat 浮点类约束：f/e/E/g/G 等转换。
	KindFloat
	// KindBool 布尔类约束：t 转换。
	KindBool
	// KindAny 任意类型约束：v 转换。
	KindAny
)

// 诊断代码。
const (
	// DiagTainted 格式串来源不可信，消费结论无法静态保证。
	DiagTainted = "tainted-format"
	// DiagMalformed 格式串本身不合法（缺少动词、括号未闭合、索引越界等）。
	DiagMalformed = "malformed-format"
	// DiagMixedModes 同一格式串内混用显式位置与顺序位置。
	DiagMixedModes = "mixed-position-modes"
	// DiagBadIndex 显式索引出现在非法位置（如 %[3]2d、%[3].2d）或索引为 0。
	DiagBadIndex = "bad-arg-index"
	// DiagTypeConflict 同一参数位置被要求满足互不兼容的类型约束。
	DiagTypeConflict = "type-conflict"
)

// Role 区分一次参数消费是用于转换本身，还是动态宽度/精度。
type Role int

const (
	// RoleValue 转换说明符的主体参数。
	RoleValue Role = iota
	// RoleWidth 动态宽度参数（*），约束恒为 KindInt。
	RoleWidth
	// RolePrecision 动态精度参数（.*），约束恒为 KindInt。
	RolePrecision
)

// Consumption 记录一次参数消费。
type Consumption struct {
	// Index 是参数的零基位置。显式模式下来自 [n]（已换算为零基），
	// 顺序模式下按消费次序递增。
	Index int
	// Explicit 为 true 表示该消费由显式索引 [n] 指定。
	Explicit bool
	// Repeated 为 true 表示该显式位置此前已被消费过（重复位置）。
	Repeated bool
	// Role 说明该参数承担转换值、动态宽度还是动态精度。
	Role Role
	// Verb 是触发本次消费的转换动词（宽度/精度消费沿用所属说明符动词）。
	Verb rune
	// Constraint 是本次消费对参数施加的类型约束。
	Constraint Kind
	// Truncating 为 true 表示字符串转换带有精度截断（如 %.3s、%.*s）。
	Truncating bool
}

// Diagnostic 记录一次可报告的问题。
type Diagnostic struct {
	Code string
	Msg  string
}

// Result 是一次格式分析的结果。
type Result struct {
	// Consumptions 按格式串中的出现次序排列的全部参数消费。
	Consumptions []Consumption
	// Diagnostics 为分析过程中发现的问题；为空表示格式与约束均无异常。
	Diagnostics []Diagnostic
	// ExplicitMode 表示格式串采用显式位置模式（出现过任意 [n] 索引）。
	ExplicitMode bool
	// MixedModes 表示两种位置模式被混用。
	MixedModes bool
	// Tainted 表示格式串来源不可信。
	Tainted bool
	// Guaranteed 表示消费结论可被静态保证；受污或格式不合法时为 false。
	Guaranteed bool

	// 顺序模式下消费到的参数总数（显式模式无意义，置 0）。
	sequentialCount int
	// 显式模式下出现过的最大零基索引（顺序模式无意义，置 -1）。
	maxExplicit int
	// 已访问的显式索引，用于判定重复位置。
	seenExplicit map[int]bool
	// 各参数位置聚合后的类型约束，用于冲突检测与宿主查询。
	constraints []Kind
}

// verbConstraint 给出转换动词对主体参数的类型约束。
func verbConstraint(verb rune) Kind {
	switch verb {
	case 'b', 'c', 'd', 'o', 'O', 'u', 'x', 'X':
		return KindInt
	case 's', 'q':
		return KindString
	case 'f', 'e', 'E', 'g', 'G', 'a', 'F':
		return KindFloat
	case 't':
		return KindBool
	case 'v', 'w':
		return KindAny
	default:
		return KindUnknown
	}
}

// compatible 判断实际类型 actual 是否满足约束 want。
func compatible(want, actual Kind) bool {
	switch want {
	case KindUnknown, KindAny:
		return true
	default:
		return want == actual
	}
}
