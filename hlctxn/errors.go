package hlctxn

import "fmt"

// ReadRestartError 是不确定性窗口读重启：事务的 readTS 被推高到
// NewReadTS 后，必须在新时间戳下安全重读。maxTS 保持不变。
type ReadRestartError struct {
	Node      int
	NewReadTS Timestamp
	MaxTS     Timestamp
}

func (e *ReadRestartError) Error() string {
	return fmt.Sprintf("hlctxn: read restart on node %d: readTS pushed to (wall=%d,logical=%d)",
		e.Node, e.NewReadTS.WallTime, e.NewReadTS.Logical)
}

// ConflictError 表示撞上无法立即赢得的 PENDING 意图，调用方应退避后整体重试事务。
type ConflictError struct {
	Key       string
	OtherTxn  uint64
	ReaderTxn uint64
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("hlctxn: conflict with pending txn %d on key %q", e.OtherTxn, e.Key)
}

// ErrAborted 在已中止事务上继续操作，或本事务被高优事务推中止时返回。
var ErrAborted = fmt.Errorf("hlctxn: transaction aborted")

// ErrCommitted 在已提交事务上继续写时返回。
var ErrCommitted = fmt.Errorf("hlctxn: transaction already committed")

// ErrTooManyRestarts 表示同一节点的不确定性重启超出预算（正常实现永不发生）。
var ErrTooManyRestarts = fmt.Errorf("hlctxn: too many read restarts")

// ErrRetryLimitExceeded 表示自动重试辅助函数达到上限仍未成功。
var ErrRetryLimitExceeded = fmt.Errorf("hlctxn: automatic retry limit exceeded")

// ErrUnknownTxn 表示意图指向的事务记录不存在（异常状态）。
var ErrUnknownTxn = fmt.Errorf("hlctxn: unknown transaction record")

// errIntentBeyondMax 是内部控制信号：意图时间戳超出 reader.maxTS，
// 调用方应跳过意图、直接按版本链做快照读。
var errIntentBeyondMax = fmt.Errorf("hlctxn: intent timestamp beyond reader maxTS")
