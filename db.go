package aries

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// TxnState 是事务状态。
type TxnState uint8

const (
	TxnRunning TxnState = iota
	TxnCommitted
	TxnAborted
)

func (s TxnState) String() string {
	switch s {
	case TxnCommitted:
		return "COMMITTED"
	case TxnAborted:
		return "ABORTED"
	}
	return "RUNNING"
}

// ErrTxnNotRunning 表示事务已不在运行态。
var ErrTxnNotRunning = errors.New("aries: transaction is not running")

// Transaction 是运行中的事务句柄。
type Transaction struct {
	ID      uint64
	State   TxnState
	LastLSN LSN
}

// DB 组合 WAL、缓冲池与事务表。
type DB struct {
	dir     string
	lm      *LogManager
	dm      *DiskManager
	bp      *BufferPool
	mu      sync.Mutex
	txns    map[uint64]*Transaction
	nextTxn uint64
	closed  bool
}

// OpenDB 打开数据库目录(不执行恢复;崩溃后请用 RecoverDB)。
func OpenDB(dir string, bufCapacity int) (*DB, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	lm, err := OpenLogManager(filepath.Join(dir, "wal.log"))
	if err != nil {
		return nil, err
	}
	dm, err := OpenDiskManager(filepath.Join(dir, "data.db"))
	if err != nil {
		lm.Close()
		return nil, err
	}
	return &DB{
		dir:     dir,
		lm:      lm,
		dm:      dm,
		bp:      NewBufferPool(dm, lm, bufCapacity),
		txns:    make(map[uint64]*Transaction),
		nextTxn: 1,
	}, nil
}

// Log 返回日志管理器。
func (db *DB) Log() *LogManager { return db.lm }

// Disk 返回磁盘管理器。
func (db *DB) Disk() *DiskManager { return db.dm }

// BufferPool 返回缓冲池。
func (db *DB) BufferPool() *BufferPool { return db.bp }

// Begin 开启事务。
func (db *DB) Begin() (*Transaction, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closed {
		return nil, ErrClosed
	}
	txn := &Transaction{ID: db.nextTxn, State: TxnRunning}
	db.nextTxn++
	lsn, err := db.lm.Append(&LogRecord{Type: TypeBegin, TxnID: txn.ID})
	if err != nil {
		return nil, err
	}
	txn.LastLSN = lsn
	db.txns[txn.ID] = txn
	return txn, nil
}

// Update 在 pageID 页的 off 处写入 data,先记日志(前像+后像)再改页。
func (db *DB) Update(txn *Transaction, pageID uint64, off int, data []byte) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closed {
		return ErrClosed
	}
	if txn.State != TxnRunning {
		return ErrTxnNotRunning
	}
	before, err := db.bp.BeforeImage(pageID, off, len(data))
	if err != nil {
		return err
	}
	rec := &LogRecord{
		Type:        TypeUpdate,
		TxnID:       txn.ID,
		PrevLSN:     txn.LastLSN,
		PageID:      pageID,
		Offset:      uint32(off),
		BeforeImage: before,
		AfterImage:  append([]byte(nil), data...),
	}
	lsn, err := db.lm.Append(rec)
	if err != nil {
		return err
	}
	if err := db.bp.ApplyLogged(pageID, off, data, lsn); err != nil {
		return err
	}
	txn.LastLSN = lsn
	return nil
}

// Commit 提交事务:写 COMMIT 并强制刷日志(提交持久性),随后写 END。
func (db *DB) Commit(txn *Transaction) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if txn.State != TxnRunning {
		return ErrTxnNotRunning
	}
	commitLSN, err := db.lm.Append(&LogRecord{Type: TypeCommit, TxnID: txn.ID, PrevLSN: txn.LastLSN})
	if err != nil {
		return err
	}
	if err := db.lm.Flush(commitLSN); err != nil {
		return err
	}
	if _, err := db.lm.Append(&LogRecord{Type: TypeEnd, TxnID: txn.ID, PrevLSN: commitLSN}); err != nil {
		return err
	}
	txn.State = TxnCommitted
	delete(db.txns, txn.ID)
	return nil
}

// Abort 主动回滚事务:沿撤销链反向扫描,逐条写 CLR 并应用前像。
func (db *DB) Abort(txn *Transaction) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if txn.State != TxnRunning {
		return ErrTxnNotRunning
	}
	last, err := db.lm.Append(&LogRecord{Type: TypeAbort, TxnID: txn.ID, PrevLSN: txn.LastLSN})
	if err != nil {
		return err
	}
	lsn := txn.LastLSN
	for lsn != NilLSN {
		rec, err := db.lm.ReadRecord(lsn)
		if err != nil {
			return err
		}
		switch rec.Type {
		case TypeUpdate:
			clr := &LogRecord{
				Type:        TypeCLR,
				TxnID:       txn.ID,
				PrevLSN:     last,
				PageID:      rec.PageID,
				Offset:      rec.Offset,
				UndoNextLSN: rec.PrevLSN,
				AfterImage:  rec.BeforeImage,
			}
			clrLSN, err := db.lm.Append(clr)
			if err != nil {
				return err
			}
			if err := db.bp.ApplyLogged(rec.PageID, int(rec.Offset), rec.BeforeImage, clrLSN); err != nil {
				return err
			}
			last = clrLSN
			lsn = rec.PrevLSN
		case TypeCLR:
			lsn = rec.UndoNextLSN
		default: // BEGIN:链结束
			lsn = NilLSN
		}
	}
	endLSN, err := db.lm.Append(&LogRecord{Type: TypeEnd, TxnID: txn.ID, PrevLSN: last})
	if err != nil {
		return err
	}
	if err := db.lm.Flush(endLSN); err != nil {
		return err
	}
	txn.State = TxnAborted
	delete(db.txns, txn.ID)
	return nil
}

// ActiveTxns 返回当前活动事务表拷贝:事务 ID -> lastLSN。
func (db *DB) ActiveTxns() map[uint64]LSN {
	db.mu.Lock()
	defer db.mu.Unlock()
	out := make(map[uint64]LSN, len(db.txns))
	for id, txn := range db.txns {
		out[id] = txn.LastLSN
	}
	return out
}

// Close 关闭文件句柄,不刷缓冲池(等价于进程退出)。
// 需要干净关闭时请使用 Shutdown。
func (db *DB) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closed {
		return nil
	}
	db.closed = true
	err1 := db.lm.Close()
	err2 := db.dm.Close()
	if err1 != nil {
		return err1
	}
	return err2
}

// Shutdown 干净关闭:刷全部脏页与日志后关闭。
func (db *DB) Shutdown() error {
	if err := db.bp.FlushAll(); err != nil {
		return err
	}
	if err := db.lm.SyncAll(); err != nil {
		return err
	}
	return db.Close()
}
