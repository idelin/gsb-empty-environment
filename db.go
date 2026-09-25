// Package pagestore implements a tiny transactional page store with
// write-ahead logging. The commit protocol is:
//
//  1. buffer the txn's records (begin, page images, commit) in memory;
//  2. write them to the WAL in one contiguous write and fsync the WAL
//     — this fsync is the commit point;
//  3. only then may page images reach the data file (checkpoint/recovery).
//
// Recovery replays only transactions that carry a complete commit record,
// applying a page record only when its LSN is newer than the on-disk page
// version, which makes replay idempotent.
package pagestore

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

const (
	pageFileName = "pages.db"
	walFileName  = "wal.log"
)

// DB is a single-writer transactional page store. All files live in the
// directory handed to Open.
type DB struct {
	dir    string
	pageF  *os.File
	walF   *os.File
	walOff int64 // end of the valid WAL prefix; next commit writes here

	mu      sync.Mutex
	closed  bool
	lsn     uint64
	txnSeq  uint64
	cache   map[uint64]*page // clean + dirty pages
	dirty   map[uint64]bool  // pages not yet in the data file
	failpts map[string]func() error
}

// Open creates (if needed) the store in dir and runs crash recovery.
// Recovery refuses to open the store when the WAL is corrupt in the middle
// or a data page is corrupt beyond what the WAL can repair.
func Open(dir string) (*DB, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	pageF, err := os.OpenFile(filepath.Join(dir, pageFileName), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	walF, err := os.OpenFile(filepath.Join(dir, walFileName), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		pageF.Close()
		return nil, err
	}
	db := &DB{
		dir:     dir,
		pageF:   pageF,
		walF:    walF,
		cache:   make(map[uint64]*page),
		dirty:   make(map[uint64]bool),
		failpts: make(map[string]func() error),
	}
	if err := db.recover(); err != nil {
		pageF.Close()
		walF.Close()
		return nil, err
	}
	if err := syncDir(dir); err != nil {
		pageF.Close()
		walF.Close()
		return nil, err
	}
	return db, nil
}

// recover scans the WAL, replays committed transactions, repairs torn or
// corrupt pages covered by the WAL, verifies every remaining data page, and
// truncates a torn WAL tail. It is idempotent and safe to run repeatedly.
func (db *DB) recover() error {
	st, err := db.walF.Stat()
	if err != nil {
		return err
	}
	data := make([]byte, st.Size())
	if _, err := db.walF.ReadAt(data, 0); err != nil && err != io.EOF {
		return err
	}
	recs, validLen, err := scanWAL(data)
	if err != nil {
		return err // mid-log corruption: never silently ignored
	}
	committed := make(map[uint64]bool)
	for _, r := range recs {
		if r.typ == recCommit {
			committed[r.txn] = true
		}
		if r.lsn > db.lsn {
			db.lsn = r.lsn
		}
	}
	for _, r := range recs {
		if r.typ != recPage || !committed[r.txn] {
			continue
		}
		var diskVer uint64
		if p, err := db.readPageFromDisk(r.pageID); err == nil {
			diskVer = p.version
		}
		// A corrupt/torn/missing page reads as version 0, so any committed
		// WAL image (LSN >= 1) repairs it. An intact page newer than the
		// record is left alone: replay is idempotent.
		if r.lsn <= diskVer {
			continue
		}
		p := &page{version: r.lsn, data: append([]byte(nil), r.data...)}
		if _, err := db.pageF.WriteAt(p.marshal(), int64(r.pageID)*pageDiskSize); err != nil {
			return err
		}
		db.cache[r.pageID] = p
	}
	if err := db.pageF.Sync(); err != nil {
		return err
	}
	// Every page now on disk must pass its checksum; anything still corrupt
	// was not covered by the WAL and means real data loss: fail loudly.
	// While scanning, also recover the LSN frontier: page versions are LSNs,
	// so the LSN counter must continue past the newest version on disk,
	// otherwise post-checkpoint commits could reuse LSNs already reflected
	// in the data file and recovery would skip them as "already applied".
	pst, err := db.pageF.Stat()
	if err != nil {
		return err
	}
	if pst.Size()%pageDiskSize != 0 {
		return fmt.Errorf("pagestore: torn page at end of data file (offset %d) not covered by WAL", pst.Size()/pageDiskSize)
	}
	for id := uint64(0); id < uint64(pst.Size())/pageDiskSize; id++ {
		p, err := db.readPageFromDisk(id)
		if err != nil {
			return fmt.Errorf("pagestore: page %d corrupt and not covered by WAL: %v", id, err)
		}
		if p.version > db.lsn {
			db.lsn = p.version
		}
	}
	// Drop the torn tail, if any, so future commits append after valid records.
	if int64(validLen) < st.Size() {
		if err := db.walF.Truncate(int64(validLen)); err != nil {
			return err
		}
		if err := db.walF.Sync(); err != nil {
			return err
		}
	}
	db.walOff = int64(validLen)
	return nil
}

// Begin starts a transaction. Writes are buffered in the txn and become
// durable and visible only through Commit.
func (db *DB) Begin() *Txn {
	db.mu.Lock()
	db.txnSeq++
	id := db.txnSeq
	db.mu.Unlock()
	return &Txn{db: db, id: id}
}

// ReadPage returns the page image and its version (LSN of the last committed
// write). A never-written page reads as zeroes with version 0.
func (db *DB) ReadPage(id uint64) ([]byte, uint64, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closed {
		return nil, 0, errors.New("pagestore: db closed")
	}
	if p, ok := db.cache[id]; ok {
		return append([]byte(nil), p.data...), p.version, nil
	}
	p, err := db.readPageFromDisk(id)
	if err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return make([]byte, PageSize), 0, nil
		}
		return nil, 0, err
	}
	return p.data, p.version, nil
}

func (db *DB) readPageFromDisk(id uint64) (*page, error) {
	buf := make([]byte, pageDiskSize)
	if _, err := db.pageF.ReadAt(buf, int64(id)*pageDiskSize); err != nil {
		return nil, err
	}
	return unmarshalPage(buf)
}

// Checkpoint flushes dirty pages to the data file, fsyncs it, and only then
// resets the WAL. Page writes always happen after the corresponding WAL
// records are durable, never before.
func (db *DB) Checkpoint() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closed {
		return errors.New("pagestore: db closed")
	}
	return db.checkpointLocked()
}

func (db *DB) checkpointLocked() error {
	ids := make([]uint64, 0, len(db.dirty))
	for id := range db.dirty {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		buf := db.cache[id].marshal()
		off := int64(id) * pageDiskSize
		if db.failActive(FailTornPageWrite) {
			// Simulate a power loss halfway through the page write.
			if _, err := db.pageF.WriteAt(buf[:len(buf)/2], off); err != nil {
				return err
			}
			return errors.New("pagestore: injected torn page write")
		}
		if _, err := db.pageF.WriteAt(buf, off); err != nil {
			return err
		}
	}
	if err := db.fail(FailCheckpointBeforeSync); err != nil {
		return err
	}
	if err := db.pageF.Sync(); err != nil {
		return err
	}
	if err := db.walF.Truncate(0); err != nil {
		return err
	}
	if err := db.walF.Sync(); err != nil {
		return err
	}
	db.walOff = 0
	db.dirty = make(map[uint64]bool)
	return syncDir(db.dir)
}

// Close checkpoints and closes the store.
func (db *DB) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closed {
		return nil
	}
	db.closed = true
	cpErr := db.checkpointLocked()
	wErr := db.walF.Close()
	pErr := db.pageF.Close()
	for _, err := range []error{cpErr, wErr, pErr} {
		if err != nil {
			return err
		}
	}
	return nil
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// Txn buffers one transaction's writes.
type Txn struct {
	db   *DB
	id   uint64
	ops  []pageOp
	done bool
}

type pageOp struct {
	id   uint64
	data []byte // PageSize bytes
}

// Set stages a full-page write. data is zero-padded to PageSize.
func (tx *Txn) Set(id uint64, data []byte) error {
	if tx.done {
		return errors.New("pagestore: txn already finished")
	}
	if len(data) > PageSize {
		return fmt.Errorf("pagestore: value %d bytes exceeds page size %d", len(data), PageSize)
	}
	buf := make([]byte, PageSize)
	copy(buf, data)
	tx.ops = append(tx.ops, pageOp{id: id, data: buf})
	return nil
}

// Commit makes the transaction durable and visible. The commit point is the
// WAL fsync: a crash before it leaves no trace of the transaction; a crash
// after it is repaired by recovery. Pages are flushed later, at checkpoint.
//
// If Commit returns an injected failpoint error, the DB handle must be
// discarded: it simulates a process crash.
func (tx *Txn) Commit() error {
	db := tx.db
	db.mu.Lock()
	defer db.mu.Unlock()
	if tx.done {
		return errors.New("pagestore: txn already finished")
	}
	tx.done = true
	if db.closed {
		return errors.New("pagestore: db closed")
	}
	if err := db.fail(FailCommitBeforeAppend); err != nil {
		return err
	}
	var buf []byte
	lsn := db.lsn
	next := func() uint64 { lsn++; return lsn }
	buf = append(buf, encodeRecord(record{lsn: next(), txn: tx.id, typ: recBegin})...)
	pageLSN := make([]uint64, len(tx.ops))
	for i, op := range tx.ops {
		pageLSN[i] = next()
		buf = append(buf, encodeRecord(record{lsn: pageLSN[i], txn: tx.id, typ: recPage, pageID: op.id, data: op.data})...)
	}
	buf = append(buf, encodeRecord(record{lsn: next(), txn: tx.id, typ: recCommit})...)
	if err := db.fail(FailCommitBeforeSync); err != nil {
		return err // crash with nothing written: txn vanishes entirely
	}
	if _, err := db.walF.WriteAt(buf, db.walOff); err != nil {
		return err
	}
	if err := db.walF.Sync(); err != nil {
		return err
	}
	db.walOff += int64(len(buf))
	db.lsn = lsn
	if err := db.fail(FailCommitAfterSync); err != nil {
		return err // crash with commit durable: recovery will replay
	}
	for i, op := range tx.ops {
		db.cache[op.id] = &page{version: pageLSN[i], data: append([]byte(nil), op.data...)}
		db.dirty[op.id] = true
	}
	return nil
}
