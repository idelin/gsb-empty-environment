package waldb

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// mutableState is the volatile transaction-manager state. It lives
// behind a pointer so Crash can discard it wholesale — a crash may
// panic out of a critical section, leaving the old mutex locked.
type mutableState struct {
	mu   sync.Mutex
	txns map[uint64]*TxnEntry // active transaction table
	dpt  map[uint32]uint64    // dirty page table: pageID -> recLSN
}

// DB is a finite-page database with ARIES-style write-ahead logging.
type DB struct {
	dir  string
	wal  *Adapter
	data *Adapter
	lm   *logMgr
	bp   *bufferPool
	lt   *lockTable
	inj  *FaultInjector

	st      *mutableState
	nextTxn uint64
}

// Open creates or opens a database in dir. The data file holds
// NumPages pages of PageSize bytes; the wal file holds the log.
func Open(dir string) (*DB, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	wal, err := OpenAdapter(filepath.Join(dir, "db.wal"), "wal")
	if err != nil {
		return nil, err
	}
	data, err := OpenAdapter(filepath.Join(dir, "db.data"), "data")
	if err != nil {
		return nil, err
	}
	db := &DB{
		dir:  dir,
		wal:  wal,
		data: data,
		lm:   newLogMgr(wal),
		inj:  newFaultInjector(),
	}
	db.bp = newBufferPool(db)
	db.lt = newLockTable()
	db.st = &mutableState{txns: map[uint64]*TxnEntry{}, dpt: map[uint32]uint64{}}
	db.nextTxn = 1
	size, err := data.Size()
	if err != nil {
		return nil, err
	}
	if size == 0 {
		if err := data.WriteAt(make([]byte, NumPages*PageSize), 0); err != nil {
			return nil, err
		}
		if err := data.Sync(); err != nil {
			return nil, err
		}
	}
	return db, nil
}

// InitPage sets a page's initial value directly (bulk load before
// logging starts; used by tests to establish original page values).
func (db *DB) InitPage(id uint32, v int64) error {
	buf := make([]byte, PageSize)
	binary.LittleEndian.PutUint64(buf[8:16], uint64(v))
	if err := db.data.WriteAt(buf, int64(id)*PageSize); err != nil {
		return err
	}
	return db.data.Sync()
}

// Begin starts a transaction and returns its ID.
func (db *DB) Begin() uint64 {
	db.st.mu.Lock()
	defer db.st.mu.Unlock()
	id := db.nextTxn
	db.nextTxn++
	lsn := db.lm.append(&LogRecord{Type: RecBegin, TxnID: id})
	db.st.txns[id] = &TxnEntry{Status: StatusRunning, LastLSN: lsn}
	return id
}

// Update adds delta to a page under the transaction's exclusive lock.
// It logs the before/after images first (WAL), then dirties the page.
func (db *DB) Update(txn uint64, pageID uint32, delta int64) error {
	if pageID >= NumPages {
		return fmt.Errorf("page %d out of range", pageID)
	}
	db.lt.lock(pageID, txn)
	db.st.mu.Lock()
	e, ok := db.st.txns[txn]
	if !ok || e.Status != StatusRunning {
		db.st.mu.Unlock()
		return fmt.Errorf("txn %d not running", txn)
	}
	p, err := db.bp.fix(pageID)
	if err != nil {
		db.st.mu.Unlock()
		return err
	}
	before := p.value
	lsn := db.lm.append(&LogRecord{
		Type: RecUpdate, TxnID: txn, PrevLSN: e.LastLSN,
		PageID: pageID, Before: before, After: before + delta,
	})
	e.LastLSN = lsn
	db.inj.hit(PtUpdateLogged)
	p.value = before + delta
	p.pageLSN = lsn
	p.dirty = true
	if _, ok := db.st.dpt[pageID]; !ok {
		db.st.dpt[pageID] = lsn
	}
	db.st.mu.Unlock()
	db.inj.hit(PtPageDirtied)
	return nil
}

// Commit makes the transaction durable. The commit point is the
// moment the commit record is persisted; only then does Commit
// report success. The end record is written afterwards and may be
// lost without affecting the outcome.
func (db *DB) Commit(txn uint64) error {
	db.st.mu.Lock()
	e, ok := db.st.txns[txn]
	if !ok || e.Status != StatusRunning {
		db.st.mu.Unlock()
		return fmt.Errorf("txn %d not running", txn)
	}
	commitLSN := db.lm.append(&LogRecord{Type: RecCommit, TxnID: txn, PrevLSN: e.LastLSN})
	e.LastLSN = commitLSN
	db.st.mu.Unlock()
	db.inj.hit(PtCommitLogged)
	db.lm.flush(commitLSN) // commit record durable => txn committed
	db.st.mu.Lock()
	e.Status = StatusCommitted
	db.st.mu.Unlock()
	db.inj.hit(PtCommitFlushed)
	db.st.mu.Lock()
	db.lm.append(&LogRecord{Type: RecEnd, TxnID: txn, PrevLSN: commitLSN})
	delete(db.st.txns, txn)
	db.st.mu.Unlock()
	db.inj.hit(PtEndLogged)
	db.lt.releaseAll(txn)
	return nil
}

// Abort rolls the transaction back, writing a CLR (with undoNextLSN)
// before each page is restored.
func (db *DB) Abort(txn uint64) error {
	db.st.mu.Lock()
	e, ok := db.st.txns[txn]
	if !ok || e.Status != StatusRunning {
		db.st.mu.Unlock()
		return fmt.Errorf("txn %d not running", txn)
	}
	abortLSN := db.lm.append(&LogRecord{Type: RecAbort, TxnID: txn, PrevLSN: e.LastLSN})
	e.LastLSN = abortLSN
	e.Status = StatusAborting
	db.undoTxnLocked(txn, PtAbortUndo)
	endLSN := db.lm.append(&LogRecord{Type: RecEnd, TxnID: txn, PrevLSN: e.LastLSN})
	e.LastLSN = endLSN
	db.lm.flush(endLSN)
	delete(db.st.txns, txn)
	db.st.mu.Unlock()
	db.lt.releaseAll(txn)
	return nil
}

// undoTxnLocked walks the transaction's log chain backwards. Every
// undone update first produces a CLR carrying undoNextLSN, then the
// page is restored. CLRs are never undone. Caller holds st.mu.
func (db *DB) undoTxnLocked(txn uint64, pt CrashPoint) {
	e := db.st.txns[txn]
	lsn := e.LastLSN
	for lsn != 0 {
		rec := db.lm.records[lsn]
		switch rec.Type {
		case RecUpdate:
			clr := &LogRecord{
				Type: RecCLR, TxnID: txn, PrevLSN: e.LastLSN,
				PageID: rec.PageID, After: rec.Before,
				UndoNextLSN: rec.PrevLSN,
			}
			clrLSN := db.lm.append(clr)
			e.LastLSN = clrLSN
			db.inj.hit(pt)
			p, err := db.bp.fix(rec.PageID)
			if err == nil {
				p.value = rec.Before
				p.pageLSN = clrLSN
				p.dirty = true
			}
			if _, ok := db.st.dpt[rec.PageID]; !ok {
				db.st.dpt[rec.PageID] = clrLSN
			}
			lsn = rec.PrevLSN
		case RecCLR:
			lsn = rec.UndoNextLSN
		case RecBegin:
			lsn = 0
		default: // abort record: follow the chain
			lsn = rec.PrevLSN
		}
	}
}

// Checkpoint writes a fuzzy checkpoint (transaction table + dirty
// page table snapshot) and makes it durable. It does not force pages,
// and it may be torn by a crash mid-flush — recovery then simply
// falls back to an earlier checkpoint or to a full log scan, so a
// usable recovery starting point always survives.
func (db *DB) Checkpoint() {
	db.st.mu.Lock()
	defer db.st.mu.Unlock()
	db.inj.hit(PtCheckpoint)
	cp := &CheckpointData{Txns: map[uint64]TxnEntry{}, DPT: map[uint32]uint64{}}
	for id, e := range db.st.txns {
		cp.Txns[id] = *e
	}
	for pg, lsn := range db.st.dpt {
		cp.DPT[pg] = lsn
	}
	lsn := db.lm.append(&LogRecord{Type: RecCheckpoint, Checkpoint: cp})
	db.lm.flush(lsn)
}

// FlushPages forces all dirty pages to disk (WAL rule enforced).
func (db *DB) FlushPages() error {
	db.inj.hit(PtPageFlush)
	return db.bp.flushAll()
}

// FlushLog forces the buffered log tail to disk.
func (db *DB) FlushLog() {
	db.lm.flushAll()
}

// Crash simulates an abrupt failure: every non-durable byte (log
// tail, buffer pool, adapter overlays) and all volatile manager
// state is discarded.
func (db *DB) Crash() {
	db.wal.Crash()
	db.data.Crash()
	db.lm.resetVolatile()
	db.bp = newBufferPool(db)
	db.lt = newLockTable()
	db.st = &mutableState{txns: map[uint64]*TxnEntry{}, dpt: map[uint32]uint64{}}
}

// Recover restarts after a crash and runs ARIES recovery. It is safe
// to call repeatedly, including after a crash during recovery itself.
func (db *DB) Recover() error {
	db.wal.Restart()
	db.data.Restart()
	if err := db.lm.recoverInit(); err != nil {
		return err
	}
	db.recover()
	return nil
}

// ReadPage reads a page's durable value directly from disk (used by
// the independent checker).
func (db *DB) ReadPage(id uint32) (int64, uint64, error) {
	buf := make([]byte, PageSize)
	if _, err := db.data.ReadAt(buf, int64(id)*PageSize); err != nil {
		return 0, 0, err
	}
	return int64(binary.LittleEndian.Uint64(buf[8:16])), binary.LittleEndian.Uint64(buf[0:8]), nil
}
