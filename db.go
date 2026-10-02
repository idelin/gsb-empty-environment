package aries

import (
	"fmt"
	"sync"
)

// DB is a minimal transaction manager with a steal/no-force buffer pool. It
// exists to produce realistic WAL streams (and to serve the demo); crash
// recovery itself lives in RecoveryEngine.
type DB struct {
	mu      sync.Mutex
	wal     *WAL
	disk    *Disk
	lm      *LockManager
	pages   map[int64]*Page // buffer pool
	recLSN  map[int64]LSN   // first LSN that dirtied a buffered page
	dirty   map[int64]bool
	lastLSN map[int64]LSN // active txn -> last LSN (the transaction table)
	nextTxn int64
}

// NewDB wires a transaction manager on top of a WAL, a disk and an arbiter.
func NewDB(wal *WAL, disk *Disk, lm *LockManager) *DB {
	return &DB{
		wal:     wal,
		disk:    disk,
		lm:      lm,
		pages:   make(map[int64]*Page),
		recLSN:  make(map[int64]LSN),
		dirty:   make(map[int64]bool),
		lastLSN: make(map[int64]LSN),
	}
}

func pageKey(id int64) string { return fmt.Sprintf("page:%d", id) }

// Begin starts a transaction and logs BEGIN.
func (db *DB) Begin() int64 {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.nextTxn++
	txn := db.nextTxn
	lsn := db.wal.Append(&LogRecord{Type: RecBegin, TxnID: txn})
	db.lastLSN[txn] = lsn
	return txn
}

// Write overwrites page[off:off+len(data)] after taking an exclusive lock on
// the page, logging an UPDATE record with before/after images first (WAL
// protocol: log before touching the page). A nil LockManager skips locking,
// which is intended for single-threaded WAL scenario construction.
func (db *DB) Write(txn, pageID int64, off uint32, data []byte) error {
	if db.lm != nil {
		if err := db.lm.Acquire(txn, pageKey(pageID), Exclusive); err != nil {
			return err
		}
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	page, ok := db.pages[pageID]
	if !ok {
		page = db.disk.ReadPage(pageID)
		db.pages[pageID] = page
	}
	old := make([]byte, len(data))
	copy(old, page.Data[off:uint32(off)+uint32(len(data))])
	rec := &LogRecord{
		Type:    RecUpdate,
		TxnID:   txn,
		PrevLSN: db.lastLSN[txn],
		PageID:  pageID,
		Offset:  off,
		OldData: old,
		NewData: append([]byte(nil), data...),
	}
	lsn := db.wal.Append(rec)
	copy(page.Data[off:], data)
	page.LSN = lsn
	if !db.dirty[pageID] {
		db.dirty[pageID] = true
		db.recLSN[pageID] = lsn
	}
	db.lastLSN[txn] = lsn
	return nil
}

// Commit logs COMMIT, forces the log, logs END and releases all locks.
func (db *DB) Commit(txn int64) {
	db.mu.Lock()
	prev := db.lastLSN[txn]
	db.wal.Append(&LogRecord{Type: RecCommit, TxnID: txn, PrevLSN: prev})
	db.wal.Sync() // commit rule: log forced before commit is acknowledged
	db.wal.Append(&LogRecord{Type: RecEnd, TxnID: txn, PrevLSN: prev})
	delete(db.lastLSN, txn)
	db.mu.Unlock()
	if db.lm != nil {
		db.lm.ReleaseAll(txn)
	}
}

// Checkpoint flushes every dirty buffer-pool page and logs a CHECKPOINT
// record carrying the current dirty-page table and transaction table.
func (db *DB) Checkpoint() {
	db.mu.Lock()
	defer db.mu.Unlock()
	// Flush every dirty page first; the checkpoint then records the DPT of
	// pages still dirty afterwards (none, under db.mu) plus the active TT.
	for id := range db.dirty {
		db.disk.WritePage(db.pages[id])
	}
	db.dirty = make(map[int64]bool)
	db.recLSN = make(map[int64]LSN)
	dpt := make(map[int64]LSN)
	tt := make(map[int64]LSN, len(db.lastLSN))
	for id, lsn := range db.lastLSN {
		tt[id] = lsn
	}
	db.wal.Append(&LogRecord{Type: RecCheckpoint, DPT: dpt, TT: tt})
	db.wal.Sync()
}
