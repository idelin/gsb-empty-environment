// Package lsm implements the memory-to-disk transition layer of an LSM
// tree: a write-ahead log, a mutable MemTable, freezing into immutable
// MemTables at a size threshold, and a single-threaded background flusher
// that sequentially persists them as Level-0 SSTable files.
package lsm

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// DefaultMemTableSize is the freeze threshold: 4 MiB.
const DefaultMemTableSize = 4 << 20

// Options configures a DB.
type Options struct {
	// Dir is the database directory directory. Created if missing.
	Dir string
	// MemTableSize is the freeze threshold in bytes. Defaults to 4 MiB.
	MemTableSize int64
	// SyncWrites fsyncs the WAL after every record when true.
	SyncWrites bool
	// Logger, if set, receives state-transition events (freeze, flush, ...).
	Logger func(format string, args ...any)
}

// DB orchestrates the WAL/MemTable/SSTable state machine.
//
// State transitions:
//
//	MemTable (active, mutable)
//	  -- size >= threshold --> Immutable MemTable (queued) + new active MemTable + WAL rotation
//	  -- background flusher (single goroutine, FIFO) --> Level-0 SSTable file
//	  -- flush committed --> paired WAL segment deleted
type DB struct {
	opt Options

	mu   sync.RWMutex // guards mem, imms, wal, ssts
	mem  *MemTable    // active MemTable receiving writes
	imms []*MemTable  // frozen MemTables waiting to be flushed, FIFO
	wal  *walWriter   // WAL segment paired with the active MemTable
	ssts []*SSTable   // live L0 tables, newest first

	nextWALID   uint64
	nextFileID  uint64
	nextTableID uint64

	flushCh chan struct{} // wakes the flusher
	doneCh  chan struct{} // closed by Close
	wg      sync.WaitGroup

	flushMu   sync.Mutex
	flushCond *sync.Cond
	rotations int // total MemTables frozen
	flushes   int // total MemTables flushed
	flushErr  error

	flushHook func() // test hook, invoked by the flusher before each flush
}

// Open opens (or creates) the database in opt.Dir, replaying any WAL
// segments left behind by a previous run.
func Open(opt Options) (*DB, error) {
	if opt.MemTableSize <= 0 {
		opt.MemTableSize = DefaultMemTableSize
	}
	if err := os.MkdirAll(opt.Dir, 0o755); err != nil {
		return nil, err
	}
	db := &DB{
		opt:        opt,
		flushCh:    make(chan struct{}, 1),
		doneCh:     make(chan struct{}),
		nextWALID:  1,
		nextFileID: 1,
	}
	db.flushCond = sync.NewCond(&db.flushMu)

	walIDs, sstIDs, err := scanDir(opt.Dir)
	if err != nil {
		return nil, err
	}

	// Replay WAL segments oldest-first. Each segment maps 1:1 to a MemTable;
	// all but the newest become immutable and are queued for flushing.
	for _, id := range walIDs {
		mt := newMemTable(id)
		path := walFilePath(opt.Dir, id)
		validEnd, err := replayWALSegment(path, func(k, v []byte) error {
			mt.Put(k, v)
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("replay %s: %w", path, err)
		}
		// Truncate any torn tail so appends resume at a clean boundary.
		if err := os.Truncate(path, validEnd); err != nil {
			return nil, err
		}
		mt.walPath = path
		db.imms = append(db.imms, mt)
		db.nextWALID = id
		db.nextTableID = id + 1
	}
	if n := len(db.imms); n > 0 {
		db.mem = db.imms[n-1]
		db.imms = db.imms[:n-1]
		db.rotations = len(db.imms)
	} else {
		db.mem = newMemTable(db.nextWALID)
		db.mem.walPath = walFilePath(opt.Dir, db.nextWALID)
		db.nextTableID = db.nextWALID + 1
	}
	w, err := openWALSegment(db.mem.walPath, opt.SyncWrites)
	if err != nil {
		return nil, err
	}
	db.wal = w

	for _, id := range sstIDs {
		t, err := openSSTable(sstFilePath(opt.Dir, id), id)
		if err != nil {
			return nil, err
		}
		db.ssts = append([]*SSTable{t}, db.ssts...) // ascending ids -> newest first
		db.nextFileID = id + 1
	}

	db.wg.Add(1)
	go db.flushLoop()
	if len(db.imms) > 0 {
		db.logf("[recover] %d immutable MemTable(s) restored from WAL, queued for flush", len(db.imms))
		db.signalFlush()
	}
	return db, nil
}

// Put writes key/val: WAL append first, then MemTable insert, then a
// threshold check that may freeze the MemTable. The freeze is O(1) (pointer
// swap + WAL rotation), so foreground writes never wait on disk flushes.
func (db *DB) Put(key, val []byte) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if err := db.wal.Append(key, val); err != nil {
		return err
	}
	db.mem.Put(key, val)
	if db.mem.Size() >= db.opt.MemTableSize {
		return db.rotateLocked()
	}
	return nil
}

// Get reads key from the active MemTable, then immutable MemTables
// (newest first), then Level-0 SSTables (newest first).
func (db *DB) Get(key []byte) ([]byte, bool, error) {
	db.mu.RLock()
	if v, ok := db.mem.Get(key); ok {
		db.mu.RUnlock()
		return v, true, nil
	}
	for i := len(db.imms) - 1; i >= 0; i-- {
		if v, ok := db.imms[i].Get(key); ok {
			db.mu.RUnlock()
			return v, true, nil
		}
	}
	ssts := db.ssts
	db.mu.RUnlock()
	for _, t := range ssts {
		v, ok, err := t.Get(key)
		if err != nil {
			return nil, false, err
		}
		if ok {
			return v, true, nil
		}
	}
	return nil, false, nil
}

// rotateLocked freezes the active MemTable, queues it for flushing, installs
// a fresh MemTable and rotates the WAL. Caller holds db.mu.
func (db *DB) rotateLocked() error {
	frozen := db.mem
	frozen.walPath = db.wal.path
	db.imms = append(db.imms, frozen)

	db.nextWALID++
	db.nextTableID++
	newPath := walFilePath(db.opt.Dir, db.nextWALID)
	w, err := openWALSegment(newPath, db.opt.SyncWrites)
	if err != nil {
		return fmt.Errorf("rotate WAL: %w", err)
	}
	old := db.wal
	old.Close() // syncs before closing; the segment stays on disk until flushed
	db.wal = w
	db.mem = newMemTable(db.nextTableID)

	db.flushMu.Lock()
	db.rotations++
	db.flushMu.Unlock()

	db.logf("[rotate] MemTable#%d frozen at %.2f MiB -> immutable queue=%d, new WAL segment %s",
		frozen.id, float64(frozen.Size())/(1<<20), len(db.imms), filepath.Base(newPath))
	db.signalFlush()
	return nil
}

func (db *DB) signalFlush() {
	select {
	case db.flushCh <- struct{}{}:
	default:
	}
}

// flushLoop is the single background flusher goroutine. It drains the
// immutable queue strictly in FIFO order, so L0 SSTables are produced
// sequentially.
func (db *DB) flushLoop() {
	defer db.wg.Done()
	for {
		select {
		case <-db.doneCh:
			db.drainImms()
			return
		case <-db.flushCh:
			db.drainImms()
		}
	}
}

func (db *DB) drainImms() {
	for {
		db.mu.Lock()
		if len(db.imms) == 0 {
			db.mu.Unlock()
			return
		}
		mt := db.imms[0]
		db.imms = db.imms[1:]
		db.mu.Unlock()

		if db.flushHook != nil {
			db.flushHook()
		}
		if err := db.flushMemTable(mt); err != nil {
			db.flushMu.Lock()
			if db.flushErr == nil {
				db.flushErr = err
			}
			db.flushMu.Unlock()
		}
		db.flushMu.Lock()
		db.flushes++
		db.flushCond.Broadcast()
		db.flushMu.Unlock()
	}
}

// flushMemTable writes one immutable MemTable as a Level-0 SSTable and, only
// after the file is durable, deletes the paired WAL segment.
func (db *DB) flushMemTable(mt *MemTable) error {
	db.mu.Lock()
	fileID := db.nextFileID
	db.nextFileID++
	db.mu.Unlock()

	path := sstFilePath(db.opt.Dir, fileID)
	n, err := writeSSTable(path, mt.sl.Iterator())
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	t, err := openSSTable(path, fileID)
	if err != nil {
		return err
	}
	db.mu.Lock()
	db.ssts = append([]*SSTable{t}, db.ssts...)
	db.mu.Unlock()

	if mt.walPath != "" {
		if err := os.Remove(mt.walPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove WAL segment %s: %w", mt.walPath, err)
		}
	}
	db.logf("[flush] MemTable#%d -> %s (%d entries, L0), WAL segment %s safely removed",
		mt.id, filepath.Base(path), n, filepath.Base(mt.walPath))
	return nil
}

// WaitForIdle blocks until every frozen MemTable has been flushed to L0.
func (db *DB) WaitForIdle() {
	db.flushMu.Lock()
	for db.flushes < db.rotations {
		db.flushCond.Wait()
	}
	db.flushMu.Unlock()
}

// Sync fsyncs the active WAL segment.
func (db *DB) Sync() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.wal.Sync()
}

// Stats is a snapshot of the state machine counters.
type Stats struct {
	Rotations        int   // MemTables frozen so far
	Flushes          int   // MemTables flushed to L0 so far
	QueuedImmutables int   // immutable MemTables still waiting
	SSTables         int   // live L0 SSTable files
	ActiveSize       int64 // approximate bytes in the active MemTable
	ActiveEntries    int
}

func (db *DB) Stats() Stats {
	db.flushMu.Lock()
	rot, flu := db.rotations, db.flushes
	db.flushMu.Unlock()
	db.mu.RLock()
	defer db.mu.RUnlock()
	return Stats{
		Rotations:        rot,
		Flushes:          flu,
		QueuedImmutables: len(db.imms),
		SSTables:         len(db.ssts),
		ActiveSize:       db.mem.Size(),
		ActiveEntries:    db.mem.Len(),
	}
}

// Close freezes any remaining data, waits for the flusher to drain, and
// releases all resources. It returns the first flush error, if any.
func (db *DB) Close() error {
	db.mu.Lock()
	if db.mem.Size() > 0 {
		db.rotateLocked()
	} else {
		db.wal.Close()
	}
	db.mu.Unlock()

	close(db.doneCh)
	db.wg.Wait()

	db.mu.Lock()
	for _, t := range db.ssts {
		t.Close()
	}
	db.ssts = nil
	db.mu.Unlock()

	db.flushMu.Lock()
	defer db.flushMu.Unlock()
	return db.flushErr
}

// debugCrash simulates a process crash: the WAL is synced and closed but no
// flush happens, so only a WAL replay can recover the data. Test use only.
func (db *DB) debugCrash() {
	db.mu.Lock()
	db.wal.Sync()
	db.wal.f.Close()
	db.mu.Unlock()
	close(db.doneCh)
	db.wg.Wait()
}

func (db *DB) logf(format string, args ...any) {
	if db.opt.Logger != nil {
		db.opt.Logger(format, args...)
	}
}

// scanDir lists WAL segment ids and SSTable ids in dir, both ascending.
func scanDir(dir string) (walIDs, sstIDs []uint64, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	for _, e := range entries {
		if id, ok := parseFileName(e.Name(), "wal-", ".log"); ok {
			walIDs = append(walIDs, id)
		}
		if id, ok := parseFileName(e.Name(), "L0-", ".sst"); ok {
			sstIDs = append(sstIDs, id)
		}
	}
	sort.Slice(walIDs, func(i, j int) bool { return walIDs[i] < walIDs[j] })
	sort.Slice(sstIDs, func(i, j int) bool { return sstIDs[i] < sstIDs[j] })
	return walIDs, sstIDs, nil
}

func parseFileName(name, prefix, suffix string) (uint64, bool) {
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
		return 0, false
	}
	id, err := strconv.ParseUint(name[len(prefix):len(name)-len(suffix)], 10, 64)
	return id, err == nil
}
