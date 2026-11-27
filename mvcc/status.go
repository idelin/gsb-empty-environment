package mvcc

import "sync"

// TxnStatus 是事务提交状态（对应 pg_xact/CLOG 中的状态位）。
type TxnStatus uint8

const (
	// StatusInProgress 表示事务仍在运行，未知状态同样按运行中处理。
	StatusInProgress TxnStatus = iota
	// StatusCommitted 表示事务已提交。
	StatusCommitted
	// StatusAborted 表示事务已中止（回滚）。
	StatusAborted
)

// TxnStatusProvider 提供事务号的提交状态查询。
type TxnStatusProvider interface {
	StatusOf(xid XID) TxnStatus
}

// Clog 是一个内存版事务提交日志（CLOG），并发安全。
type Clog struct {
	mu     sync.RWMutex
	status map[XID]TxnStatus
}

// NewClog 创建空的内存 CLOG。
func NewClog() *Clog {
	return &Clog{status: make(map[XID]TxnStatus)}
}

// Set 记录某个事务号的最终状态。
func (c *Clog) Set(xid XID, status TxnStatus) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.status[xid] = status
}

// StatusOf 返回事务状态；未登记的事务号一律视为运行中，
// 这与 PostgreSQL 对缺失 CLOG 状态的保守处理一致。
func (c *Clog) StatusOf(xid XID) TxnStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.status[xid]
}
