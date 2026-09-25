package vrec

import "errors"

// 生命周期与语义错误。全部为显式、可断言的哨兵错误。
var (
	// ErrNotFound 键不存在，或快照版本上该键已被删除（墓碑）。
	ErrNotFound = errors.New("vrec: record not found")
	// ErrTxnClosed 事务/读视图已结束后再次调用，或重复关闭。
	ErrTxnClosed = errors.New("vrec: transaction already closed")
	// ErrTxnReentrant 同一个事务实例被多个 goroutine 重入使用。
	ErrTxnReentrant = errors.New("vrec: transaction is not reentrant (concurrent use of same txn)")
	// ErrFaulted 存储此前遭遇不可判定故障（写入/同步失败、注入崩溃），必须重开。
	ErrFaulted = errors.New("vrec: store faulted; reopen required")
	// ErrClosed 数据库已关闭。
	ErrClosed = errors.New("vrec: database closed")
	// ErrEmptyKey 键不允许为空。
	ErrEmptyKey = errors.New("vrec: empty key")
	// ErrSimCrash 注入点请求的“进程崩溃”语义错误；随后所有操作返回 ErrFaulted。
	ErrSimCrash = errors.New("vrec: simulated process crash (failpoint)")
)
