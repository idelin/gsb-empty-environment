package netsec

import "fmt"

// Action 是判定表对一次系统调用请求给出的处置动作。
type Action uint8

const (
	// Allow 放行系统调用，严格度最低。
	Allow Action = iota
	// Notify 放行系统调用并通知宿主（例如记录审计事件）。
	Notify
	// Deny 拒绝系统调用并返回错误码，不终止线程。
	Deny
	// Kill 立即终止触发系统调用的线程（或整个进程），严格度最高。
	Kill
)

// rank 返回动作的严格度等级，数值越大越严格。
// 等级顺序固定为：终止(3) > 拒绝(2) > 通知(1) > 允许(0)。
func (a Action) rank() int {
	switch a {
	case Kill:
		return 3
	case Deny:
		return 2
	case Notify:
		return 1
	case Allow:
		return 0
	default:
		// 未知动作按最严格处理，避免因脏数据意外放行。
		return 4
	}
}

// Valid 报告动作是否为四个受支持的动作之一。
func (a Action) Valid() bool {
	return a == Allow || a == Notify || a == Deny || a == Kill
}

// String 返回动作的可读名称。
func (a Action) String() string {
	switch a {
	case Allow:
		return "allow"
	case Notify:
		return "notify"
	case Deny:
		return "deny"
	case Kill:
		return "kill"
	default:
		return fmt.Sprintf("action(%d)", uint8(a))
	}
}

// stricterThan 报告 a 的严格度是否高于 b。
func (a Action) stricterThan(b Action) bool {
	return a.rank() > b.rank()
}
