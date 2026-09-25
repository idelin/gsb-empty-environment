package diskdeque

import "errors"

var (
	// ErrEmpty 表示队列为空。
	ErrEmpty = errors.New("diskdeque: empty")
	// ErrTxDone 表示事务已提交或回滚，不能再使用。
	ErrTxDone = errors.New("diskdeque: transaction already done")
	// ErrCrashed 表示注入点在提交点之后制造了“崩溃”，句柄已失效，
	// 必须重新 Open 目录执行恢复。
	ErrCrashed = errors.New("diskdeque: handle aborted by simulated crash; reopen the directory")
	// ErrClosed 表示 Deque 已关闭。
	ErrClosed = errors.New("diskdeque: closed")
	// ErrBlockSpaceExhausted 表示环绕块编号空间中没有可分配的空闲编号。
	ErrBlockSpaceExhausted = errors.New("diskdeque: block id space exhausted")
)
