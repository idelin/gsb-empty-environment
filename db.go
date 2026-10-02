package aries

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// DefaultPageSize is used when Open is given a non-positive size.
const DefaultPageSize = 4096

// DB is a single-machine transactional store with WAL-based
// ARIES recovery.
type DB struct {
	mu       sync.Mutex
	dir      string
	lm       *LogManager
	dm       *DiskManager
	bp       *BufferPool
	txnTable map[TxID]*TxnInfo
	dpt      map[PageID]LSN // dirty page table: page -> recLSN
	nextTxID TxID
	finished bool

	// LastRecovery describes what the most recent restart did.
	LastRecovery *RecoveryReport
}

// Open opens (or creates) the database in dir and runs ARIES recovery
// over the WAL found there.
func Open(dir string, pageSize int) (*DB, error) {
	if pageSize <= PageHeaderSize {
		pageSize = DefaultPageSize
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	lm, err := openLogManager(filepath.Join(dir, "wal.log"))
	if err != nil {
		return nil, err
	}
	dm, err := openDiskManager(filepath.Join(dir, "data.db"), pageSize)
	if err != nil {
		lm.Close()
		return nil, err
	}
	db := &DB{
		dir:      dir,
		lm:       lm,
		dm:       dm,
		bp:       newBufferPool(dm, pageSize),
		txnTable: make(map[TxID]*TxnInfo),
		dpt:      make(map[PageID]LSN),
		nextTxID: lm.maxTxID + 1,
	}
	db.LastRecovery = db.recover()
	return db, nil
}

// Begin starts a new transaction.
func (db *DB) Begin() *Txn {
	db.mu.Lock()
	defer db.mu.Unlock()
	id := db.nextTxID
	db.nextTxID++
	db.txnTable[id] = &TxnInfo{Status: StatusRunning, LastLSN: NilLSN}
	return &Txn{db: db, id: id}
}

// Checkpoint appends a checkpoint record (snapshot of the transaction
// table and DPT), forces the log, then flushes all dirty pages.
func (db *DB) Checkpoint() error {
	db.mu.Lock()
	rec := &LogRecord{
		Type:     RecCheckpoint,
		TxnTable: make(map[TxID]TxnInfo),
		DPT:      make(map[PageID]LSN),
	}
	for id, info := range db.txnTable {
		rec.TxnTable[id] = *info
	}
	for pid, lsn := range db.dpt {
		rec.DPT[pid] = lsn
	}
	db.mu.Unlock()

	lsn := db.lm.Append(rec)
	if err := db.lm.Flush(lsn); err != nil {
		return err
	}
	if _, err := db.bp.FlushAll(); err != nil {
		return err
	}
	if err := db.dm.Sync(); err != nil {
		return err
	}
	db.mu.Lock()
	db.dpt = make(map[PageID]LSN)
	db.mu.Unlock()
	return nil
}

// ReadPage returns a copy of the usable payload of a page.
func (db *DB) ReadPage(id PageID) ([]byte, error) {
	p, err := db.bp.Fetch(id)
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(p.Data()))
	copy(out, p.Data())
	return out, nil
}

// LogRecords returns all WAL records in order (for tests and demos).
func (db *DB) LogRecords() []*LogRecord { return db.lm.Records() }

// Close performs a clean shutdown: checkpoint, then close files.
func (db *DB) Close() error {
	if db.finished {
		return fmt.Errorf("db already closed")
	}
	db.finished = true
	if err := db.Checkpoint(); err != nil {
		return err
	}
	db.lm.Close()
	return db.dm.Close()
}

// Crash simulates a sudden power loss: the WAL (already forced to
// stable storage by the WAL protocol) survives, but every dirty page
// still sitting in the buffer pool is lost. The DB handle is dead
// afterwards; call Open again to recover.
func (db *DB) Crash() {
	if db.finished {
		return
	}
	db.finished = true
	db.lm.Sync() // log records that reached the OS survive the crash
	db.lm.Close()
	db.dm.Close() // data file closed WITHOUT flushing the buffer pool
	db.bp = nil
	db.txnTable = nil
	db.dpt = nil
}
