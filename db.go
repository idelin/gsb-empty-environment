// Package kvdb is an append-only log-structured key-value store built
// only on the Go standard library.
//
// Writes (including deletions, which append tombstone records) go to the
// active segment. On startup every live segment is scanned to rebuild an
// in-memory index. When the active segment grows past MaxSegmentBytes it
// is sealed and a new one is started. Compaction merges all sealed
// segments into a fresh segment keeping only currently visible records,
// commits the new layout through MANIFEST, and reclaims the old files
// once no reader pins them anymore.
package kvdb

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

var (
	// ErrNotFound is returned by Get for missing or deleted keys.
	ErrNotFound = errors.New("kvdb: key not found")
	// ErrClosed is returned by operations on a closed DB.
	ErrClosed = errors.New("kvdb: database is closed")
)

const (
	defaultMaxSegmentBytes = 16 << 20
	segmentSuffix          = ".seg"
	compactSuffix          = ".compact"
)

// Options configures a DB. The zero value is valid.
type Options struct {
	// MaxSegmentBytes is the size at which the active segment is sealed
	// and rotated. Defaults to 16 MiB.
	MaxSegmentBytes int64
	// SyncWrites fsyncs the active segment after every acknowledged
	// write. Required for power-failure durability of individual writes.
	SyncWrites bool
	// Injector installs fault-injection hooks; nil disables injection.
	Injector Injector
}

// location points at one record inside a segment file.
type location struct {
	segID     uint64
	offset    int64
	length    int64
	tombstone bool
}

// segment is an open segment file. refs counts in-flight readers; an
// obsolete segment is only closed and unlinked once refs reaches zero,
// so concurrent readers never observe a reclaimed file.
type segment struct {
	id       uint64
	path     string
	file     *os.File
	size     int64
	refs     int
	sealed   bool
	obsolete bool
}

// DB is a log-structured key-value store rooted at a single directory.
type DB struct {
	dir  string
	opts Options

	mu        sync.RWMutex
	compactMu sync.Mutex

	segments map[uint64]*segment
	order    []uint64 // live (non-obsolete) segment ids, sorted
	active   *segment
	index    map[string]location
	nextID   uint64
	closed   bool
}

// Open creates or recovers a store rooted at dir. Recovery reads the
// MANIFEST (if any), deletes orphaned files left by interrupted
// compactions, replays every live segment to rebuild the index, and
// truncates a torn tail left by a crash mid-append.
func Open(dir string, opts *Options) (*DB, error) {
	o := Options{MaxSegmentBytes: defaultMaxSegmentBytes}
	if opts != nil {
		o = *opts
	}
	if o.MaxSegmentBytes <= 0 {
		o.MaxSegmentBytes = defaultMaxSegmentBytes
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	db := &DB{
		dir:      dir,
		opts:     o,
		segments: make(map[uint64]*segment),
		index:    make(map[string]location),
		nextID:   1,
	}

	m, err := readManifest(dir)
	if err != nil {
		return nil, err
	}
	onDisk, err := listSegmentIDs(dir)
	if err != nil {
		return nil, err
	}

	// Live set = committed segments still on disk, plus any segment newer
	// than the committed maximum. The latter covers segments created by
	// rotation or by a compaction that crashed after renaming its output
	// into place but before committing the MANIFEST; replaying such a
	// segment is idempotent because it only duplicates visible records.
	live := make(map[uint64]bool)
	if m == nil {
		for id := range onDisk {
			live[id] = true
		}
	} else {
		var maxCommitted uint64
		for _, id := range m.Segments {
			if id > maxCommitted {
				maxCommitted = id
			}
		}
		for _, id := range m.Segments {
			if onDisk[id] {
				live[id] = true
			}
		}
		for id := range onDisk {
			if id > maxCommitted {
				live[id] = true
			}
		}
	}

	// Sweep orphans: uncommitted segments, compaction scratch files and a
	// stale MANIFEST.tmp.
	entries, err := ioutil.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		name := e.Name()
		switch {
		case strings.HasSuffix(name, compactSuffix) || name == manifestTmp:
			os.Remove(filepath.Join(dir, name))
		case strings.HasSuffix(name, segmentSuffix):
			if id, perr := parseSegmentName(name); perr == nil && !live[id] {
				os.Remove(filepath.Join(dir, name))
			}
		}
	}

	var ids []uint64
	for id := range live {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		seg, err := db.openAndReplay(id)
		if err != nil {
			return nil, err
		}
		db.segments[id] = seg
		db.order = append(db.order, id)
		if id >= db.nextID {
			db.nextID = id + 1
		}
	}

	active, err := db.createSegment(db.nextID)
	if err != nil {
		return nil, err
	}
	db.nextID++
	db.segments[active.id] = active
	db.order = append(db.order, active.id)
	db.active = active
	if err := db.writeManifestLocked(); err != nil {
		return nil, err
	}
	return db, nil
}

// openAndReplay opens one segment and folds its records into the index,
// truncating the file at the first corrupt or partial record (torn tail).
func (db *DB) openAndReplay(id uint64) (*segment, error) {
	path := filepath.Join(db.dir, segmentFileName(id))
	f, err := os.OpenFile(path, os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	seg := &segment{id: id, path: path, file: f, sealed: true}
	var off int64
	br := bufio.NewReaderSize(f, 64<<10)
	for {
		key, value, tombstone, err := readRecord(br)
		if err == io.EOF {
			break
		}
		if err != nil {
			if terr := f.Truncate(off); terr != nil {
				f.Close()
				return nil, terr
			}
			if serr := f.Sync(); serr != nil {
				f.Close()
				return nil, serr
			}
			break
		}
		recLen := int64(recordHeaderSize + len(key) + len(value))
		db.index[string(key)] = location{segID: id, offset: off, length: recLen, tombstone: tombstone}
		off += recLen
	}
	seg.size = off
	return seg, nil
}

func (db *DB) createSegment(id uint64) (*segment, error) {
	path := filepath.Join(db.dir, segmentFileName(id))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_EXCL, 0644)
	if err != nil {
		return nil, err
	}
	if err := syncDir(db.dir); err != nil {
		f.Close()
		os.Remove(path)
		return nil, err
	}
	return &segment{id: id, path: path, file: f}, nil
}

// Put stores value under key, appending a record to the active segment.
func (db *DB) Put(key, value []byte) error {
	return db.append(key, value, false)
}

// Delete removes key by appending a tombstone record.
func (db *DB) Delete(key []byte) error {
	return db.append(key, nil, true)
}

func (db *DB) append(key, value []byte, tombstone bool) error {
	if len(key) == 0 {
		return errEmptyKey
	}
	if tombstone {
		value = nil
	}
	rec := encodeRecord(key, value, tombstone)

	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closed {
		return ErrClosed
	}
	if db.active.size > 0 && db.active.size+int64(len(rec)) > db.opts.MaxSegmentBytes {
		if err := db.rotateLocked(); err != nil {
			return err
		}
	}
	off := db.active.size
	if _, err := db.active.file.WriteAt(rec, off); err != nil {
		return err
	}
	db.active.size += int64(len(rec))
	if db.opts.SyncWrites {
		if err := db.inject(InjectBeforeAppendSync); err != nil {
			return err
		}
		if err := db.active.file.Sync(); err != nil {
			return err
		}
	}
	db.index[string(key)] = location{
		segID:     db.active.id,
		offset:    off,
		length:    int64(len(rec)),
		tombstone: tombstone,
	}
	return nil
}

// rotateLocked seals the active segment and starts a new one, committing
// the new layout through the MANIFEST. Callers must hold db.mu.
func (db *DB) rotateLocked() error {
	if err := db.active.file.Sync(); err != nil {
		return err
	}
	db.active.sealed = true
	seg, err := db.createSegment(db.nextID)
	if err != nil {
		return err
	}
	db.nextID++
	db.segments[seg.id] = seg
	db.order = append(db.order, seg.id)
	db.active = seg
	return db.writeManifestLocked()
}

// Get returns the current value for key, or ErrNotFound. The segment
// backing the record is pinned for the duration of the read so a
// concurrent compaction cannot reclaim the file underneath us.
func (db *DB) Get(key []byte) ([]byte, error) {
	db.mu.RLock()
	if db.closed {
		db.mu.RUnlock()
		return nil, ErrClosed
	}
	loc, ok := db.index[string(key)]
	if !ok || loc.tombstone {
		db.mu.RUnlock()
		return nil, ErrNotFound
	}
	seg := db.segments[loc.segID]
	seg.refs++
	db.mu.RUnlock()

	defer func() {
		db.mu.Lock()
		seg.refs--
		db.sweepLocked(seg)
		db.mu.Unlock()
	}()

	rec := make([]byte, loc.length)
	if _, err := seg.file.ReadAt(rec, loc.offset); err != nil {
		return nil, err
	}
	_, value, tombstone, err := decodeRecord(rec)
	if err != nil {
		return nil, err
	}
	if tombstone {
		return nil, ErrNotFound
	}
	out := make([]byte, len(value))
	copy(out, value)
	return out, nil
}

// Sync fsyncs the active segment and the directory, making all previously
// acknowledged writes durable against power failure.
func (db *DB) Sync() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closed {
		return ErrClosed
	}
	if err := db.inject(InjectBeforeAppendSync); err != nil {
		return err
	}
	if err := db.active.file.Sync(); err != nil {
		return err
	}
	return syncDir(db.dir)
}

// Close flushes and closes all segment files. It does not remove
// obsolete-but-pinned files; recovery cleans them up next open.
func (db *DB) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closed {
		return nil
	}
	db.closed = true
	var firstErr error
	if db.active != nil {
		firstErr = db.active.file.Sync()
	}
	for _, seg := range db.segments {
		if err := seg.file.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// sweepLocked closes and unlinks an obsolete segment once no reader pins
// it. Callers must hold db.mu.
func (db *DB) sweepLocked(seg *segment) {
	if seg.obsolete && seg.refs == 0 && seg != db.active {
		seg.file.Close()
		os.Remove(seg.path)
		delete(db.segments, seg.id)
	}
}

func segmentFileName(id uint64) string {
	return fmt.Sprintf("%06d%s", id, segmentSuffix)
}

func parseSegmentName(name string) (uint64, error) {
	if !strings.HasSuffix(name, segmentSuffix) {
		return 0, errCorruptRecord
	}
	return strconv.ParseUint(strings.TrimSuffix(name, segmentSuffix), 10, 64)
}

func listSegmentIDs(dir string) (map[uint64]bool, error) {
	entries, err := ioutil.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	ids := make(map[uint64]bool)
	for _, e := range entries {
		if id, err := parseSegmentName(e.Name()); err == nil {
			ids[id] = true
		}
	}
	return ids, nil
}
