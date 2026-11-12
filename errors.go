package netsec

import "fmt"

// LoopError 表示别名链出现闭环（某个名字在本链中已出现过）。
type LoopError struct {
	Name string
	Path []string
}

func (e *LoopError) Error() string {
	return fmt.Sprintf("netsec: CNAME 循环：名字 %s 已出现在路径 %v 中", e.Name, e.Path)
}

// ContradictionError 表示同一名字给出了互不相容的别名指向，
// 或同一应答中一个名字出现多条不同目标的 CNAME，或 CNAME 与末端数据共存。
type ContradictionError struct {
	Name    string
	TargetA string
	TargetB string
	Reason  string
}

func (e *ContradictionError) Error() string {
	if e.TargetB == "" {
		return fmt.Sprintf("netsec: 名字 %s 的别名相互矛盾：%s", e.Name, e.Reason)
	}
	return fmt.Sprintf("netsec: 名字 %s 的别名相互矛盾：%s（%s 与 %s）",
		e.Name, e.Reason, e.TargetA, e.TargetB)
}

// IncompleteChainError 表示追踪在取得完整答案前中断（典型为途中超时）。
// Partial 保留已经确认的信任路径，组合器不会据此编造否定结论。
type IncompleteChainError struct {
	QueryName string
	Partial   *PartialPath
	Err       error
}

func (e *IncompleteChainError) Error() string {
	return fmt.Sprintf("netsec: 别名链在解析 %s 时中断：%v", e.QueryName, e.Err)
}

func (e *IncompleteChainError) Unwrap() error { return e.Err }

// ChainLimitError 表示别名链超过允许的最大跳数。
type ChainLimitError struct {
	Limit int
	Name  string
}

func (e *ChainLimitError) Error() string {
	return fmt.Sprintf("netsec: 别名链超过最大跳数 %d（停在 %s）", e.Limit, e.Name)
}

// 协议层面的静态错误。
var (
	errEmptyReply      = errStr("netsec: 解析器返回了空应答")
	errQueryMismatch   = errStr("netsec: 应答与查询的名字或类型不一致")
	errUnexpectedRcode = errStr("netsec: 应答码既不是肯定也不是可缓存的否定")
)

type errStr string

func (e errStr) Error() string { return string(e) }
