package netsec

import "errors"

// ErrInfeasible 表示给定读自边与允许重排关系下不存在满足全部约束的执行。
var ErrInfeasible = errors.New("netsec: 不存在满足先行发生与每位置写序的执行")

// ErrLimitExceeded 表示参考全序实现的事件数超过全排列可承受的上限。
var ErrLimitExceeded = errors.New("netsec: 事件数超出穷举全序参考实现上限")

// ReadFrom 是一条读自边：读事件 Reader 读到写事件 Writer 的值。
// 写事件可以是普通写，也可以是位置的初始写。
type ReadFrom struct {
	Reader int
	Writer int
}

// FailPredicate 由宿主提供，判定一个前缀（完整执行 Trace 的前 prefixLen 个
// 事件）是否已经触发断言失败，例如读到了尚未发布的秘密缓冲区。
//
// complete 为 true 时 prefixLen == len(Trace)。谓词只能依赖前缀中已发生的
// 读及其读自边，不得假设未发生的事件。
type FailPredicate func(prog *Program, trace Trace, prefixLen int) bool

// Query 是一次执行枚举的全部输入。
type Query struct {
	Program *Program

	// ReadFroms 给出每个读事件的读自边；允许只给一部分，其余读默认读初始写。
	ReadFroms []ReadFrom

	// MayReorder 显式声明允许重排的相邻线程内事件对（Pair.First 在线程序中
	// 先于 Pair.Second）。为 nil 时使用 Program.MayReorder 的默认规则。
	MayReorder []Pair

	// Fail 是断言失败谓词；为 nil 时任何连贯执行都算失败（用于纯可达性检查）。
	Fail FailPredicate

	// Limit 限制返回的执行数量；<= 0 表示使用默认上限。负值 -1 表示不限制。
	Limit int
}

// Trace 是一个具体执行：事件下标的一个全序（不包含固定在最前的初始写，
// 初始写总在所有真实事件之前）。
type Trace []int

// Position 返回事件 id 在该全序中的位置，不存在时返回 -1。
func (t Trace) Position(id int) int {
	for i, ev := range t {
		if ev == id {
			return i
		}
	}
	return -1
}

// Witness 是断言失败的最短见证。
type Witness struct {
	// Trace 是完整的连贯执行。
	Trace Trace
	// FailAt 是断言首次失败的前缀长度（1..len(Trace)）。
	FailAt int
	// Prefix 是触发失败的最短前缀。
	Prefix Trace
}

// Result 汇总一次枚举的结果。
type Result struct {
	// Traces 是全部（或前 Limit 个）满足约束的连贯执行。
	Traces []Trace
	// Truncated 为 true 表示达到 Limit，可能还有未列出的执行。
	Truncated bool
}
