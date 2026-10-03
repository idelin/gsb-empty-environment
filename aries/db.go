package aries

import "sync"

// Database is a minimal transaction engine used to generate WAL traffic:
// updates, commits and fuzzy checkpoints against a buffer pool and disk.
type Database struct {
	WAL  *WAL
	Disk *Disk
	BP   *BufferPool

	mu      sync.Mutex
	lastLSN map[TxID]LSN
	dpt     map[PageID]LSN
}

// NewDatabase wires a WAL, disk and buffer pool together.
func NewDatabase(wal *WAL, disk *Disk) *Database {
	db := &Database{
		WAL:     wal,
		Disk:    disk,
		BP:      NewBufferPool(disk),
		lastLSN: make(map[TxID]LSN),
		dpt:     make(map[PageID]LSN),
	}
	db.BP.onFlush = func(id PageID) {
		db.mu.Lock()
		delete(db.dpt, id)
		db.mu.Unlock()
	}
	return db
}

// Update logs and applies a page update on behalf of tx.
func (db *Database) Update(tx TxID, page PageID, before, after []byte) LSN {
	db.mu.Lock()
	prev := db.lastLSN[tx]
	db.mu.Unlock()

	rec := &LogRecord{
		TxID:    tx,
		Type:    TypeUpdate,
		PrevLSN: prev,
		PageID:  page,
		Before:  append([]byte(nil), before...),
		After:   append([]byte(nil), after...),
	}
	lsn := db.WAL.Append(rec)

	p := db.BP.Fetch(page)
	p.Data = append([]byte(nil), after...)
	p.PageLSN = lsn
	p.Dirty = true

	db.mu.Lock()
	db.lastLSN[tx] = lsn
	if _, ok := db.dpt[page]; !ok {
		db.dpt[page] = lsn
	}
	db.mu.Unlock()
	return lsn
}

// Commit writes COMMIT, forces the log, then writes END.
func (db *Database) Commit(tx TxID) {
	db.mu.Lock()
	prev := db.lastLSN[tx]
	db.mu.Unlock()

	commitLSN := db.WAL.Append(&LogRecord{TxID: tx, Type: TypeCommit, PrevLSN: prev})
	_ = db.WAL.Flush()
	db.WAL.Append(&LogRecord{TxID: tx, Type: TypeEnd, PrevLSN: commitLSN})

	db.mu.Lock()
	delete(db.lastLSN, tx)
	db.mu.Unlock()
}

// Checkpoint writes a fuzzy checkpoint: BEGIN, then END carrying a
// snapshot of the transaction table and dirty page table.
func (db *Database) Checkpoint() {
	beginLSN := db.WAL.Append(&LogRecord{Type: TypeBeginCheckpoint})

	db.mu.Lock()
	tt := make(map[TxID]TTEntry, len(db.lastLSN))
	for tx, lsn := range db.lastLSN {
		tt[tx] = TTEntry{Status: StatusRunning, LastLSN: lsn}
	}
	dpt := make(map[PageID]LSN, len(db.dpt))
	for p, lsn := range db.dpt {
		dpt[p] = lsn
	}
	db.mu.Unlock()

	db.WAL.Append(&LogRecord{
		Type:     TypeEndCheckpoint,
		BeginLSN: beginLSN,
		TT:       tt,
		DPT:      dpt,
	})
	_ = db.WAL.Flush()
}
