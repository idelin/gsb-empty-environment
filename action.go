package policycheck

import (
	"bytes"
	"fmt"
)

// Action 是沙箱动作。字符串值保持与宿主策略配置一致。
type Action string

const (
	ActionKill  Action = "KILL"
	ActionTrap  Action = "TRAP"
	ActionErrno Action = "ERRNO"
	ActionTrace Action = "TRACE"
	ActionLog   Action = "LOG"
	ActionAllow Action = "ALLOW"
)

// Rank 返回动作优先级，数值越大优先级越高。
func (a Action) Rank() int {
	switch a {
	case ActionKill:
		return 60
	case ActionTrap:
		return 50
	case ActionErrno:
		return 40
	case ActionTrace:
		return 30
	case ActionLog:
		return 20
	case ActionAllow:
		return 10
	default:
		return -1
	}
}

// Valid 报告动作是否为六种受支持动作之一。
func (a Action) Valid() bool { return a.Rank() >= 0 }

// StrongerThan 报告 a 是否比 b 更严格。
func (a Action) StrongerThan(b Action) bool { return a.Rank() > b.Rank() }

// RelaxedRelativeTo 报告 a 是否比参考动作 want 更宽松。
func (a Action) RelaxedRelativeTo(want Action) bool { return a.Rank() < want.Rank() }

// Payload 是与动作一起传递的数据载荷。
//
// 不同动作可使用不同字段；等价校验会比较全部字段，避免 ERRNO、
// TRAP、TRACE 或通知数据被相邻规则覆盖或串号。
type Payload struct {
	Errno  uint64 `json:"errno,omitempty"`
	Signal uint64 `json:"signal,omitempty"`
	Trace  uint64 `json:"trace,omitempty"`
	Code   uint64 `json:"code,omitempty"`
	// PassthroughNR 非空时表示需要原样传递/补发的调用号。
	PassthroughNR *uint64 `json:"passthrough_nr,omitempty"`
	Data          []byte  `json:"data,omitempty"`
}

// ErrnoPayload 构造 ERRNO 载荷。
func ErrnoPayload(errno uint64) Payload { return Payload{Errno: errno} }

// Equal 精确比较载荷。
func (p Payload) Equal(q Payload) bool {
	if p.Errno != q.Errno || p.Signal != q.Signal || p.Trace != q.Trace || p.Code != q.Code {
		return false
	}
	if !bytes.Equal(p.Data, q.Data) {
		return false
	}
	if (p.PassthroughNR == nil) != (q.PassthroughNR == nil) {
		return false
	}
	if p.PassthroughNR != nil && *p.PassthroughNR != *q.PassthroughNR {
		return false
	}
	return true
}

func (p Payload) String() string {
	return fmt.Sprintf("Payload{errno=%d signal=%d trace=%d code=%d nr=%v data=%x}",
		p.Errno, p.Signal, p.Trace, p.Code, p.PassthroughNR, p.Data)
}

// Decision 是规则链或 DAG 终点给出的完整决定。
type Decision struct {
	Action  Action   `json:"action"`
	Payload Payload  `json:"payload,omitempty"`
	Audit   []string `json:"audit,omitempty"`
	Notify  []string `json:"notify,omitempty"`
}

// AllowDecision 是默认 ALLOW 决定。
func AllowDecision() Decision { return Decision{Action: ActionAllow} }

// Clone 返回深拷贝，防止求值过程中共享底层切片。
func (d Decision) Clone() Decision {
	out := d
	out.Audit = append([]string(nil), d.Audit...)
	out.Notify = append([]string(nil), d.Notify...)
	if d.Payload.Data != nil {
		out.Payload.Data = append([]byte(nil), d.Payload.Data...)
	}
	if d.Payload.PassthroughNR != nil {
		nr := *d.Payload.PassthroughNR
		out.Payload.PassthroughNR = &nr
	}
	return out
}

func stringSet(s []string) map[string]int {
	m := make(map[string]int, len(s))
	for _, v := range s {
		m[v]++
	}
	return m
}

// MissingObligations 返回 want 中存在、而 got 未以足够重数保留的义务。
func MissingObligations(want, got []string) []string {
	have := stringSet(got)
	var missing []string
	for _, item := range want {
		if have[item] > 0 {
			have[item]--
			continue
		}
		missing = append(missing, item)
	}
	return missing
}
