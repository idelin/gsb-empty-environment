// Package msgproc 提供消息处理的两层状态跟踪：
// 第一层为签收（Receipt），第二层为业务执行（Business）。
// 签收成功仅表示消息被受理并签发处理票据，不代表业务执行成功。
package msgproc

import "time"

// Message 是待处理的消息。ID 由调用者分配，用于签收幂等。
type Message struct {
	ID      string
	Payload []byte
}

// ReceiptState 签收层状态。
type ReceiptState string

const (
	// ReceiptAcked 消息已签收，处理票据已签发。
	ReceiptAcked ReceiptState = "ACKED"
)

// BusinessState 业务层状态。
type BusinessState string

const (
	// BizPending 已签收，业务尚未执行。
	BizPending BusinessState = "PENDING"
	// BizRunning 业务正在执行。
	BizRunning BusinessState = "RUNNING"
	// BizSucceeded 业务执行成功，结果已记录。
	BizSucceeded BusinessState = "SUCCEEDED"
	// BizFailed 业务执行失败（处理器返回错误）。
	BizFailed BusinessState = "FAILED"
	// BizTimeout 业务执行超时。
	BizTimeout BusinessState = "TIMEOUT"
	// BizResultLost 业务执行过但结果丢失，调用者无法得知真实结果。
	BizResultLost BusinessState = "RESULT_LOST"
)

// Phase 是面向调用者的三阶段视图，由两层状态推导得出。
type Phase int

const (
	// PhaseUnknown 票据不存在。
	PhaseUnknown Phase = iota
	// PhaseAcknowledged 阶段一：签收完成，业务尚无确定结果（未执行或执行中）。
	PhaseAcknowledged
	// PhaseResultUnknown 阶段二：业务执行过，但结果丢失，真实结果未知。
	PhaseResultUnknown
	// PhaseResultKnown 阶段三：业务结果已确定（成功、失败或超时）。
	PhaseResultKnown
)

// String 返回阶段的可读描述。
func (p Phase) String() string {
	switch p {
	case PhaseAcknowledged:
		return "ACKNOWLEDGED"
	case PhaseResultUnknown:
		return "RESULT_UNKNOWN"
	case PhaseResultKnown:
		return "RESULT_KNOWN"
	default:
		return "UNKNOWN"
	}
}

// Ticket 是签收成功后签发的处理票据，后续查询与重试均凭票据进行。
type Ticket struct {
	ID        string
	MessageID string
	IssuedAt  time.Time
}

// Attempt 记录一次业务执行尝试，重试时按序追加，历史完整保留。
type Attempt struct {
	Seq       int
	StartedAt time.Time
	EndedAt   time.Time
	Outcome   BusinessState // SUCCEEDED / FAILED / TIMEOUT / RESULT_LOST
	Err       string
}

// StatusView 是查询接口返回的只读状态视图。
type StatusView struct {
	Ticket      Ticket
	Receipt     ReceiptState
	Business    BusinessState
	Attempts    []Attempt
	Result      []byte
	ResultKnown bool
}

// Phase 推导当前所处的三阶段之一。
func (v StatusView) Phase() Phase {
	switch v.Business {
	case BizPending, BizRunning:
		return PhaseAcknowledged
	case BizResultLost:
		return PhaseResultUnknown
	case BizSucceeded, BizFailed, BizTimeout:
		return PhaseResultKnown
	default:
		return PhaseUnknown
	}
}
