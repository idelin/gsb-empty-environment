package aries

import (
	"fmt"
	"sync"
)

// TxnEntry is one row of the transaction table.
type TxnEntry struct {
	ID      uint64
	Status  TxnStatus
	LastLSN uint64
}

// DB is a minimal transactional runtime used to generate WAL scenarios.
// It keeps a small page cache, a dirty page table and a transaction table,
// and follows the WAL rule: log records are durable before the
// corresponding page can be flushed.
type DB struct {
	mu      sync.Mutex
	wal     *WAL
	store   *PageStore
	cache   map[uint64]*SparsePage
	dpt     map[uint64]uint64
	tt      map[uint64]*TxnEntry
	nextTxn uint64
}

// OpenDB creates a runtime over the given WAL and page files.
func OpenDB(walPath, pagePath string) (*DB, error) {
	wal, err := OpenWAL(walPath)
	if err != nil {
		return nil, err
	}
	store, err := OpenPageStore(pagePath)
	if err != nil {
		wal.Close()
		return nil, err
	}
	return &DB{
		wal:   wal,
		store: store,
		cache: map[uint64]*SparsePage{},
		dpt:   map[uint64]uint64{},
		tt:    map[uint64]*TxnEntry{},
	}, nil
}

// Begin starts a transaction and returns its id.
func (db *DB) Begin() uint64 {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.nextTxn++
	db.tt[db.nextTxn] = &TxnEntry{ID: db.nextTxn}
	return db.nextTxn
}

// Update sets one cell of a page inside txn, logging an UPDATE record
// first. It returns the previous cell value.
func (db *DB) Update(txn, pageID uint64, off uint32, val uint32) (uint32, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	t := db.tt[txn]
	if t == nil {
		return 0, fmt.Errorf("db: unknown txn %d", txn)
	}
	page, ok := db.cache[pageID]
	if !ok {
		var err error
		page, err = db.store.ReadPage(pageID)
		if err != nil {
			return 0, err
		}
		db.cache[pageID] = page
	}
	old := page.Get(off)
	lsn := db.wal.Append(&LogRecord{
		Type: LogUpdate, TxnID: txn, PrevLSN: t.LastLSN,
		PageID: pageID, Offset: off, OldValue: old, NewValue: val,
	})
	t.LastLSN = lsn
	page.Set(off, val)
	page.LSN = lsn
	if _, ok := db.dpt[pageID]; !ok {
		db.dpt[pageID] = lsn
	}
	return old, nil
}

// Commit logs COMMIT (durable via WAL sync) and then END.
func (db *DB) Commit(txn uint64) {
	db.mu.Lock()
	defer db.mu.Unlock()
	t := db.tt[txn]
	if t == nil {
		return
	}
	lsn := db.wal.Append(&LogRecord{Type: LogCommit, TxnID: txn, PrevLSN: t.LastLSN})
	db.wal.Append(&LogRecord{Type: LogEnd, TxnID: txn, PrevLSN: lsn})
	delete(db.tt, txn)
}

// Abort logs an ABORT record; the actual rollback is performed by the
// recovery engine (or left for it after a crash).
func (db *DB) Abort(txn uint64) {
	db.mu.Lock()
	defer db.mu.Unlock()
	t := db.tt[txn]
	if t == nil {
		return
	}
	lsn := db.wal.Append(&LogRecord{Type: LogAbort, TxnID: txn, PrevLSN: t.LastLSN})
	t.LastLSN = lsn
	t.Status = StatusAborting
}

// Checkpoint writes a fuzzy checkpoint record containing a snapshot of the
// current dirty page table and transaction table.
func (db *DB) Checkpoint() uint64 {
	db.mu.Lock()
	defer db.mu.Unlock()
	c := &CheckpointData{
		DPT: make(map[uint64]uint64, len(db.dpt)),
		TT:  make(map[uint64]CheckpointTxn, len(db.tt)),
	}
	for pid, recLSN := range db.dpt {
		c.DPT[pid] = recLSN
	}
	for tid, e := range db.tt {
		c.TT[tid] = CheckpointTxn{LastLSN: e.LastLSN, Status: e.Status}
	}
	return db.wal.Append(&LogRecord{Type: LogCheckpoint, Checkpoint: c})
}

// FlushPages writes the cached pages with the given ids to the page store
// and removes them from the dirty page table.
func (db *DB) FlushPages(ids ...uint64) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	for _, id := range ids {
		page, ok := db.cache[id]
		if !ok {
			continue
		}
		if err := db.store.WritePage(id, page); err != nil {
			return err
		}
		delete(db.dpt, id)
	}
	return nil
}

// Crash simulates an abrupt crash: buffered (unflushed) pages are lost,
// only the WAL and previously flushed pages survive.
func (db *DB) Crash() {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.wal.Close()
	db.store.Close()
	db.cache = nil
}
