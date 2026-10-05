package msgproc

import "time"

// Phase 是调用者可见的两层状态中的阶段。
// 第一层是签收（PhaseAcked），第二层是业务执行
// （PhaseExecuting/PhaseSucceeded/PhaseFailed）。
// PhaseUnknown 表示业务结局无法确定（超时或崩溃中断），
// 调用者必须把它与明确的失败区分开。
type Phase int

const (
	PhaseAcked Phase = iota
	PhaseExecuting
	PhaseSucceeded
	PhaseFailed
	PhaseUnknown
)

func (p Phase) String() string {
	switch p {
	case PhaseAcked:
		return "ACKED"
	case PhaseExecuting:
		return "EXECUTING"
	case PhaseSucceeded:
		return "SUCCEEDED"
	case PhaseFailed:
		return "FAILED"
	case PhaseUnknown:
		return "UNKNOWN"
	}
	return "INVALID"
}

// Outcome 是单次业务尝试的结局。
type Outcome int

const (
	OutcomeSuccess Outcome = iota
	OutcomeFailure
	OutcomeTimeout
	OutcomeInterrupted
)

func (o Outcome) String() string {
	switch o {
	case OutcomeSuccess:
		return "SUCCESS"
	case OutcomeFailure:
		return "FAILURE"
	case OutcomeTimeout:
		return "TIMEOUT"
	case OutcomeInterrupted:
		return "INTERRUPTED"
	}
	return "INVALID"
}

// Attempt 记录一次业务执行尝试，重试时追加而非覆盖。
type Attempt struct {
	Seq        int
	StartedAt  time.Time
	FinishedAt time.Time
	Outcome    Outcome
	Err        string
}

// Status 是 Query 返回的票据视图，包含两层状态与全部尝试历史。
type Status struct {
	TicketID   string
	MessageID  string
	Phase      Phase
	Attempts   []Attempt
	Result     interface{}
	ResultLost bool
}
