// Package db ties the WAL, buffer pool and metrics registry together into a
// transactional store: begin/update/commit/abort plus checkpoints. It is the
// "normal processing" half of the system that the recovery engine repairs.
package db

import (
	"errors"
	"sync"

	"aries/metrics"
	"aries/storage"
	"aries/wal"
)

// ErrTxnDone is returned when operating on a finished transaction.
var ErrTxnDone = errors.New("db: transaction already finished")

// DB is a transactional page store.
type DB struct {
	Log     *wal.LogManager
	Pool    *storage.BufferPool
	Metrics *metrics.Registry

	mu      sync.Mutex
	nextTxn int64
	txns    map[int64]*Txn
}

// Open opens the database files. reg may be nil.
func Open(dataPath, logPath string, poolCapacity int, reg *metrics.Registry) (*DB, error) {
	dm, err := storage.OpenDiskManager(dataPath)
	if err != nil {
		return nil, err
	}
	lm, err := wal.Open(logPath)
	if err != nil {
		dm.Close()
		return nil, err
	}
	return &DB{
		Log:     lm,
		Pool:    storage.NewBufferPool(dm, lm, poolCapacity, reg),
		Metrics: reg,
		txns:    make(map[int64]*Txn),
	}, nil
}

func (db *DB) count(name string) {
	if db.Metrics != nil {
		db.Metrics.Counter(name).Inc()
	}
}

// Close flushes everything and closes the files.
func (db *DB) Close() error {
	if err := db.Pool.FlushAll(); err != nil {
		return err
	}
	return db.Log.Close()
}

// Begin starts a new transaction.
func (db *DB) Begin() *Txn {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.nextTxn++
	t := &Txn{db: db, id: db.nextTxn, prevLSN: wal.LSNInvalid}
	db.txns[t.id] = t
	db.count("txn.begun")
	return t
}

// ActiveTxns returns a snapshot of the transaction table (txnID -> lastLSN).
func (db *DB) ActiveTxns() map[int64]wal.TxnInfo {
	db.mu.Lock()
	defer db.mu.Unlock()
	tt := make(map[int64]wal.TxnInfo, len(db.txns))
	for id, t := range db.txns {
		tt[id] = wal.TxnInfo{Status: wal.StatusRunning, LastLSN: t.prevLSN}
	}
	return tt
}

// Checkpoint writes BEGIN/END checkpoint records (the END record carries the
// current transaction table and dirty page table), then flushes all pages.
func (db *DB) Checkpoint() error {
	db.mu.Lock()
	beginLSN, err := db.Log.Append(&wal.LogRecord{Type: wal.TypeBeginCheckpoint, PrevLSN: wal.LSNInvalid})
	if err != nil {
		db.mu.Unlock()
		return err
	}
	tt := make(map[int64]wal.TxnInfo, len(db.txns))
	for id, t := range db.txns {
		tt[id] = wal.TxnInfo{Status: wal.StatusRunning, LastLSN: t.prevLSN}
	}
	dpt := db.Pool.DirtyPages()
	db.mu.Unlock()
	if _, err := db.Log.Append(&wal.LogRecord{
		Type:    wal.TypeEndCheckpoint,
		PrevLSN: beginLSN,
		DPT:     dpt,
		TT:      tt,
	}); err != nil {
		return err
	}
	if err := db.Log.Flush(); err != nil {
		return err
	}
	db.count("txn.checkpoints")
	return db.Pool.FlushAll()
}

// Txn is a single transaction.
type Txn struct {
	db      *DB
	id      int64
	prevLSN wal.LSN
	updates []wal.LSN // LSNs of this txn's UPDATE records, in order
	undone  int       // how many of them have been compensated by CLRs
	done    bool
}

// ID returns the transaction id.
func (t *Txn) ID() int64 { return t.id }

// LastLSN returns the LSN of the transaction's most recent log record.
func (t *Txn) LastLSN() wal.LSN { return t.prevLSN }

// Update writes data at offset within pageID, logging before/after images.
func (t *Txn) Update(pageID int32, offset uint32, data []byte) error {
	if t.done {
		return ErrTxnDone
	}
	page, err := t.db.Pool.FetchPage(pageID)
	if err != nil {
		return err
	}
	before := make([]byte, len(data))
	copy(before, page.Data[offset:offset+uint32(len(data))])
	lsn, err := t.db.Log.Append(&wal.LogRecord{
		TxnID:   t.id,
		Type:    wal.TypeUpdate,
		PrevLSN: t.prevLSN,
		PageID:  pageID,
		Offset:  offset,
		Before:  before,
		After:   append([]byte(nil), data...),
	})
	if err != nil {
		t.db.Pool.UnpinPage(pageID, false)
		return err
	}
	t.prevLSN = lsn
	t.updates = append(t.updates, lsn)
	copy(page.Data[offset:], data)
	page.LSN = lsn
	t.db.Pool.UnpinPage(pageID, true)
	t.db.count("txn.updates")
	return nil
}

// Commit logs COMMIT, flushes the log (durability), then logs END.
func (t *Txn) Commit() error {
	if t.done {
		return ErrTxnDone
	}
	commitLSN, err := t.db.Log.Append(&wal.LogRecord{
		TxnID: t.id, Type: wal.TypeCommit, PrevLSN: t.prevLSN,
	})
	if err != nil {
		return err
	}
	t.prevLSN = commitLSN
	if err := t.db.Log.Flush(); err != nil {
		return err
	}
	if _, err := t.db.Log.Append(&wal.LogRecord{
		TxnID: t.id, Type: wal.TypeEnd, PrevLSN: t.prevLSN,
	}); err != nil {
		return err
	}
	t.finish()
	t.db.count("txn.committed")
	return nil
}

// BeginAbort logs the ABORT record. Exported so crash-recovery tests can
// simulate a transaction that dies mid-abort.
func (t *Txn) BeginAbort() error {
	if t.done {
		return ErrTxnDone
	}
	lsn, err := t.db.Log.Append(&wal.LogRecord{
		TxnID: t.id, Type: wal.TypeAbort, PrevLSN: t.prevLSN,
	})
	if err != nil {
		return err
	}
	t.prevLSN = lsn
	return nil
}

// AbortOne undoes the most recent not-yet-undone update by writing a CLR and
// applying the before-image. It returns false when nothing remains to undo.
func (t *Txn) AbortOne() (bool, error) {
	if t.done {
		return false, ErrTxnDone
	}
	if t.undone >= len(t.updates) {
		return false, nil
	}
	updateLSN := t.updates[len(t.updates)-1-t.undone]
	rec, err := t.db.Log.ReadRecord(updateLSN)
	if err != nil {
		return false, err
	}
	clrLSN, err := t.db.Log.Append(&wal.LogRecord{
		TxnID:       t.id,
		Type:        wal.TypeCLR,
		PrevLSN:     t.prevLSN,
		UndoNextLSN: rec.PrevLSN,
		PageID:      rec.PageID,
		Offset:      rec.Offset,
		After:       rec.Before,
	})
	if err != nil {
		return false, err
	}
	page, err := t.db.Pool.FetchPage(rec.PageID)
	if err != nil {
		return false, err
	}
	copy(page.Data[rec.Offset:], rec.Before)
	page.LSN = clrLSN
	t.db.Pool.UnpinPage(rec.PageID, true)
	t.prevLSN = clrLSN
	t.undone++
	t.db.count("txn.clrs")
	return true, nil
}

// Abort rolls the transaction back completely and logs END.
func (t *Txn) Abort() error {
	if err := t.BeginAbort(); err != nil {
		return err
	}
	for {
		more, err := t.AbortOne()
		if err != nil {
			return err
		}
		if !more {
			break
		}
	}
	if _, err := t.db.Log.Append(&wal.LogRecord{
		TxnID: t.id, Type: wal.TypeEnd, PrevLSN: t.prevLSN,
	}); err != nil {
		return err
	}
	t.finish()
	t.db.count("txn.aborted")
	return nil
}

func (t *Txn) finish() {
	t.done = true
	t.db.mu.Lock()
	delete(t.db.txns, t.id)
	t.db.mu.Unlock()
}
