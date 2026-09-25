package mvcc

import "errors"

var (
	// ErrStoreClosed 表示 Store 已关闭。
	ErrStoreClosed = errors.New("mvcc: store closed")
	// ErrWriterActive 表示已有一个未结束的写事务（单写约束）。
	ErrWriterActive = errors.New("mvcc: another write transaction is active")
	// ErrTxClosed 表示事务已提交/回滚/关闭，禁止重入或重复关闭。
	ErrTxClosed = errors.New("mvcc: transaction already closed")
)
