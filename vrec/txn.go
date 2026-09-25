package vrec

import (
	"io"
	"sort"
	"sync"
)

// mut 是事务内针对一个键的最终（最后一次）修改。
type mut struct {
	val  []byte
	tomb bool
}

// Txn 是写事务。同一时刻只允许存在一个活跃写事务（单写者）；写事务之间、
// 写事务与 GC 之间在提交/回收的短暂临界区内串行。写事务同时能读取：
// 它先看到自己尚未提交的修改，否则看到开始时的已提交快照。
type Txn struct {
	db   *DB
	snap int64
	id   uint64

	pending map[string]mut

	mu     sync.Mutex // 保护 closed 与 pending
	active bool       // 是否有操作正在进行（拒绝重入）
	closed bool
}

// writerToken 把写者串行化：容量为 1 的信号量。
// 写事务在整个生命周期内占据它，因此 GC 与其互斥；
// 但普通读事务完全不经过它，长读从不阻塞写。

// Begin 开始一个写事务。已有未结束的写事务时返回 ErrTxnReentrant。
func (db *DB) Begin() (*Txn, error) {
	select {
	case db.writerToken <- struct{}{}:
	default:
		return nil, ErrTxnReentrant
	}

	db.wmu.Lock()
	defer db.wmu.Unlock()
	if err := db.healthy(); err != nil {
		<-db.writerToken
		return nil, err
	}
	// 写事务以 writer 身份登记快照，使自身的起始快照也成为 GC 水位候选；
	// 这保证 GC 不会回收它在事务内 Get 可能读取的已提交历史。
	id := db.register(db.head, true)
	return &Txn{db: db, snap: db.head, id: id, pending: make(map[string]mut)}, nil
}

func (tx *Txn) enter() error {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if tx.closed {
		return ErrTxnClosed
	}
	if tx.active {
		return ErrTxnReentrant // 同一事务不允许重入/并发使用
	}
	tx.active = true
	return nil
}

func (tx *Txn) leave() {
	tx.mu.Lock()
	tx.active = false
	tx.mu.Unlock()
}

// Get 读取键的当前逻辑值：优先返回本事务内未提交的修改，否则返回事务快照上的
// 已提交值。已删除/不存在返回 ErrNotFound。
func (tx *Txn) Get(key string) ([]byte, error) {
	if err := tx.enter(); err != nil {
		return nil, err
	}
	defer tx.leave()

	if err := tx.db.healthy(); err != nil {
		return nil, err
	}
	tx.db.wmu.RLock()
	defer tx.db.wmu.RUnlock()
	if m, ok := tx.pending[key]; ok {
		if m.tomb {
			return nil, ErrNotFound
		}
		return append([]byte(nil), m.val...), nil
	}
	n := searchChain(tx.db.chains[key], tx.snap)
	if n == nil || n.tomb {
		return nil, ErrNotFound
	}
	return append([]byte(nil), n.val...), nil
}

// Put 写入（或覆盖）一个键。
func (tx *Txn) Put(key string, value []byte) error {
	if key == "" {
		return ErrEmptyKey
	}
	if err := tx.enter(); err != nil {
		return err
	}
	defer tx.leave()
	if err := tx.db.healthy(); err != nil {
		return err
	}
	tx.pending[key] = mut{val: append([]byte(nil), value...)}
	return nil
}

// Delete 删除一个键，记录墓碑。
func (tx *Txn) Delete(key string) error {
	if key == "" {
		return ErrEmptyKey
	}
	if err := tx.enter(); err != nil {
		return err
	}
	defer tx.leave()
	if err := tx.db.healthy(); err != nil {
		return err
	}
	tx.pending[key] = mut{tomb: true}
	return nil
}

// Rollback 放弃事务；不产生版本、不写日志。重复关闭返回 ErrTxnClosed。
func (tx *Txn) Rollback() error {
	tx.mu.Lock()
	if tx.closed {
		tx.mu.Unlock()
		return ErrTxnClosed
	}
	tx.closed = true
	tx.mu.Unlock()
	tx.db.wmu.Lock()
	tx.db.unregister(tx.id)
	tx.db.wmu.Unlock()
	<-tx.db.writerToken
	return nil
}

// Commit 持久化并发布事务：
//  1. 分配新版本，把每个修改作为独立、CRC 自校验的帧追加到 WAL 并 fsync；
//  2. fsync 成功后才更新内存链并推进 head（提交点）。
//
// 空事务直接结束，不占用版本。重复提交返回 ErrTxnClosed。
func (tx *Txn) Commit() error {
	tx.mu.Lock()
	if tx.closed {
		tx.mu.Unlock()
		return ErrTxnClosed
	}
	tx.closed = true
	tx.mu.Unlock()
	defer func() { <-tx.db.writerToken }()

	tx.db.wmu.Lock()
	defer tx.db.wmu.Unlock()

	if err := tx.db.healthy(); err != nil {
		tx.db.unregister(tx.id)
		return err
	}

	if len(tx.pending) == 0 {
		tx.db.unregister(tx.id)
		return nil
	}

	newVer := tx.db.head + 1
	keys := make([]string, 0, len(tx.pending))
	for k := range tx.pending {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// 注入点：帧落盘之前。此处故障等价于进程在提交前终止，可干净中止，
	// 已提交状态与文件都不受影响。
	if err := tx.db.fail(StageTxnWrite, newVer, keys, true); err != nil {
		tx.db.unregister(tx.id)
		return err
	}

	// 临界区：记录追加前的日志长度，以便帧写入中途失败时回滚追加的字节，
	// 保持“日志只包含已确认帧”。
	savedLen := tx.db.walLen
	for _, k := range keys {
		m := tx.pending[k]
		fr := frame{key: k, ver: newVer, tombstone: m.tomb, value: m.val}
		if err := appendFrame(tx.db.walFile, fr, false); err != nil {
			// 写失败处于不可判定区：可能部分字节已离开本进程。截断到已知
			// 好的长度；若截断也失败则标记 faulted，交由重开恢复。
			if terr := tx.db.walFile.Truncate(savedLen); terr != nil {
				tx.db.faulted.Store(true)
				tx.db.unregister(tx.id)
				return terr
			}
			if serr := tx.db.walFile.Sync(); serr != nil {
				tx.db.faulted.Store(true)
				tx.db.unregister(tx.id)
				return serr
			}
			tx.db.walLen = savedLen
			tx.db.faulted.Store(true)
			tx.db.unregister(tx.id)
			return err
		}
	}

	// 注入点：帧已 write 且 fsync、内存尚未发布。此处若“进程消失”，
	// 重启重放会重新发布该版本——这正是已提交事务的持久化语义。
	if err := tx.db.fail(StageTxnSync, newVer, keys, false); err != nil {
		tx.db.unregister(tx.id)
		return err
	}

	// 提交点：发布到内存链并推进 head。
	for _, k := range keys {
		m := tx.pending[k]
		tx.db.chains[k] = append(tx.db.chains[k], &node{
			ver:  newVer,
			val:  append([]byte(nil), m.val...),
			tomb: m.tomb,
		})
	}
	tx.db.head = newVer
	tx.db.walLen, _ = tx.db.walFile.Seek(0, io.SeekEnd)
	tx.db.unregister(tx.id)
	return nil
}

// Update 在一个写事务中执行 fn；fn 返回 nil 时提交，否则回滚并返回该错误。
// fn 内部不要再调用 tx.Commit/Rollback。
func (db *DB) Update(fn func(*Txn) error) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
