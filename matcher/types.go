package matcher

import (
	"time"
)

// Event 是参与匹配的不可变事件。
//
// At 是事件时间（而非处理时间），匹配、时间窗与水位线判断全部基于事件时间。
// At 必须非零。Attrs 携带用于键相等约束与否定段判定的事件属性。
type Event struct {
	ID    string
	Type  string
	At    time.Time
	Attrs map[string]string
}

// Window 是左闭右开的相对时间窗 [Low, High)。
//
// 当 BaseAlias 为空时，窗口相对于规则零号段绑定的锚点事件；否则相对于
// BaseAlias 指定的前序正段绑定事件。Low 必须非负且 High 必须大于 Low。
type Window struct {
	Low       time.Duration
	High      time.Duration
	BaseAlias string
}

// Step 描述规则序列中的一个段。
//
// Alias 是段在整条规则内的唯一别名，用于键相等约束和窗口 BaseAlias 引用。
// EventType 指定该段匹配的事件类型。Negate 为 true 时该段是否定段：窗口
// [Window.Low, Window.High) 内出现任何满足键相等约束的同类型事件即构成违反。
type Step struct {
	Alias     string
	EventType string
	Window    Window
	Negate    bool
}

// KeyEquality 声明一组段别名，要求这些段最终绑定的事件在 Key 指定的属性上
// 取值完全相同。否定段也可以出现在组内，用于限定“同一主体的成功登录”等语义。
type KeyEquality struct {
	Key     string
	Aliases []string
}

// Rule 是一条可重复求值的序列匹配规则。
type Rule struct {
	ID        string
	Steps     []Step
	EqualKeys []KeyEquality
}

// Status 是匹配实例（一条从零号段锚点出发的匹配分支）的生命周期状态。
type Status int

const (
	// StatusTentative 表示暂定：前缀已匹配，但仍有否定段等待水位线确认，
	// 或后续正段尚未绑定，此时不能对外定案。
	StatusTentative Status = iota + 1
	// StatusAlerted 表示告警：所有正段均已绑定且全部否定段均已确认。
	StatusAlerted
	// StatusRevoked 表示撤销：暂定期间否定窗口内到达了匹配事件，暂定结论作废。
	StatusRevoked
	// StatusExpired 表示过期：某个正段窗口在水位线达到右端后仍未绑定事件。
	StatusExpired
)

// String 返回状态的可读名称。
func (s Status) String() string {
	switch s {
	case StatusTentative:
		return "TENTATIVE"
	case StatusAlerted:
		return "ALERTED"
	case StatusRevoked:
		return "REVOKED"
	case StatusExpired:
		return "EXPIRED"
	default:
		return "UNKNOWN"
	}
}

// OutputKind 标识引擎输出记录的类别。
type OutputKind int

const (
	// OutputTentative 表示一条新匹配实例进入暂定状态。
	OutputTentative OutputKind = iota + 1
	// OutputAlert 表示匹配实例定案为告警。
	OutputAlert
	// OutputRevoke 表示暂定实例被迟到但仍在水位线之前的否定事件撤销。
	OutputRevoke
	// OutputExpire 表示匹配实例因正段窗口关闭而过期。
	OutputExpire
)

// String 返回输出类别的可读名称。
func (k OutputKind) String() string {
	switch k {
	case OutputTentative:
		return "TENTATIVE"
	case OutputAlert:
		return "ALERT"
	case OutputRevoke:
		return "REVOKE"
	case OutputExpire:
		return "EXPIRE"
	default:
		return "UNKNOWN"
	}
}

// Match 是对外暴露的匹配实例快照。
//
// MatchID 由规则 ID 与全部正段证据事件 ID 确定性派生，同一条匹配在暂定、
// 告警、撤销、过期各阶段保持同一 ID。Evidence 按规则段顺序记录正段绑定的
// 事件 ID。RevokedBy 仅在 Status 为 [StatusRevoked] 时非空，记录触发撤销的
// 否定事件 ID。
type Match struct {
	MatchID   string
	RuleID    string
	Status    Status
	Evidence  []string
	RevokedBy string
}

// Output 是引擎在推送事件或推进水位线时产生的状态变更记录。
type Output struct {
	Kind      OutputKind
	Match     Match
	Reason    string
	EventTime time.Time
}

// RejectedEvent 描述一次被引擎拒绝接收的事件。
//
// 拒绝不改变引擎内部状态。Reason 给出可展示的拒绝原因（迟到事件会明确说明
// 事件时间与当前水位线）。
type RejectedEvent struct {
	Event  Event
	Reason string
}

// BatchItem 是批处理参考运行器的单条输入。
//
// 同一批次内事件按切片顺序视为到达顺序；Watermark 非零时表示在处理本项事件
// 之前先把水位线推进到该时刻。
type BatchItem struct {
	Event     Event
	Watermark time.Time
}
