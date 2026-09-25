package vrec

import "sync/atomic"

// Reader 是固定版本的读事务（快照读）。BeginReader 之后无论数据库如何写入，
// 它读到的内容都不会改变。
//
// Reader 不持有跨操作锁，因此一个长期打开的 Reader 不会阻塞任何写入。
// 它只在 GC 的水位计算中登记了一个版本号；请在用完后调用 Close 释放。
type Reader struct {
	db   *DB
	snap int64
	id   uint64

	inUse  atomic.Bool // 防止同一实例被并发重入
	closed atomic.Bool
}

// BeginReader 在当前已提交版本上开始一个只读事务。
func (db *DB) BeginReader() (*Reader, error) {
	db.wmu.Lock()
	defer db.wmu.Unlock()
	if err := db.healthy(); err != nil {
		return nil, err
	}
	id := db.register(db.head, false)
	return &Reader{db: db, snap: db.head, id: id}, nil
}

// Snapshot 返回该读事务固定下来的版本号。
func (r *Reader) Snapshot() int64 { return r.snap }

// Get 读取快照版本上 key 的值；已删除或不存在返回 ErrNotFound。
func (r *Reader) Get(key string) ([]byte, error) {
	if r.closed.Load() {
		return nil, ErrTxnClosed
	}
	if !r.inUse.CompareAndSwap(false, true) {
		return nil, ErrTxnReentrant
	}
	defer r.inUse.Store(false)

	if err := r.db.healthy(); err != nil {
		return nil, err
	}
	r.db.wmu.RLock()
	defer r.db.wmu.RUnlock()
	if r.closed.Load() {
		return nil, ErrTxnClosed
	}
	n := searchChain(r.db.chains[key], r.snap)
	if n == nil || n.tomb {
		return nil, ErrNotFound
	}
	return append([]byte(nil), n.val...), nil
}

// Close 注销该读事务。重复关闭返回 ErrTxnClosed。
func (r *Reader) Close() error {
	if !r.closed.CompareAndSwap(false, true) {
		return ErrTxnClosed
	}
	r.db.wmu.Lock()
	defer r.db.wmu.Unlock()
	r.db.unregister(r.id)
	return r.db.healthy()
}
