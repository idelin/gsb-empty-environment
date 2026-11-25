package permcompile

import "errors"

// SessionID 标识一次导入会话。事件与读取都绑定到具体会话，
// 跨会话重放在绑定校验阶段即被拒绝。
type SessionID string

// EventKind 区分已认证事件的用途。
type EventKind int

const (
	// EventDecision 决定某个二选一门选择哪个选项（Option 为选项标识）。
	EventDecision EventKind = iota
	// EventFallback 声明条件回退触发，Gate 为回退节点门标识。
	EventFallback
	// EventCancel 取消 Gate 所寻址的整棵子树（必须仍可取消）。
	EventCancel
)

// Event 是绑定本会话与计划摘要的已认证外部事件。
// 库只接受内存中的事件对象；签名真实性由 Authenticator 回调判定。
type Event struct {
	Kind    EventKind
	Session SessionID
	Digest  PlanDigest // 必须等于编译期计划摘要
	Gate    string     // 门名/门标识
	Option  string     // EventDecision 使用
	Nonce   string     // 会话内唯一，防重放
	Raw     []byte     // 原始签名载荷，交给 Authenticator
}

// Authenticator 由宿主实现：校验事件签名与绑定关系。
// 返回 false 时库拒绝该事件，且不改变任何运行时状态。
type Authenticator func(ev Event) bool

// 库在绑定校验之外使用的哨兵错误。
var (
	errExpired         = errors.New("permcompile: session expired")
	errNoWorlds        = errors.New("permcompile: no viable interpretation remains")
	errUnknownGate     = errors.New("permcompile: event references unknown gate")
	errGateSettled     = errors.New("permcompile: gate already settled")
	errCannotCancel    = errors.New("permcompile: target cannot be canceled in this state")
	errBadFallback     = errors.New("permcompile: fallback event on non-fallback gate")
	errUnknownOption   = errors.New("permcompile: unknown choice option")
	errReplay          = errors.New("permcompile: event nonce replayed within session")
	errBindSession     = errors.New("permcompile: event bound to another session")
	errBindDigest      = errors.New("permcompile: event bound to another plan digest")
	errUnauthenticated = errors.New("permcompile: event failed authenticator")
)
