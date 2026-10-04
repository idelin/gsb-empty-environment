package msgproc

import "errors"

// ErrSimulatedCrash 在注入“签收后崩溃”故障时由 Acknowledge 返回。
// 注意：此时签收已持久化、票据已签发，调用者恢复后重复签收会拿到同一票据。
var ErrSimulatedCrash = errors.New("msgproc: simulated crash after ack")

// Faults 故障注入点。 nil 字段表示不注入对应故障。
type Faults struct {
	// CrashAfterAck 返回 true 时，签收持久化后立即模拟进程崩溃：
	// Acknowledge 返回 ErrSimulatedCrash，业务不会执行。
	CrashAfterAck func(messageID string) bool
	// HangInExecute 返回 true 时，业务执行挂起直至超时。
	HangInExecute func(ticketID string) bool
	// LoseResult 返回 true 时，业务执行成功但结果被丢弃，
	// 状态转为 RESULT_LOST。
	LoseResult func(ticketID string) bool
}

func (f Faults) crashAfterAck(messageID string) bool {
	return f.CrashAfterAck != nil && f.CrashAfterAck(messageID)
}

func (f Faults) hangInExecute(ticketID string) bool {
	return f.HangInExecute != nil && f.HangInExecute(ticketID)
}

func (f Faults) loseResult(ticketID string) bool {
	return f.LoseResult != nil && f.LoseResult(ticketID)
}
