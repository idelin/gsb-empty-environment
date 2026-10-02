// Package lsm implements the delete-semantics kernel of an LSM tree:
// tombstone markers, read-time masking for point gets and range scans,
// and a lifecycle cleaner (compaction) that physically eliminates
// tombstones once they sink to the bottom-most level.
package lsm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

var errCorrupt = errors.New("lsm: corrupt sstable")

// Options configures a DB.
type Options struct {
	// MemtableLimit is the approximate memtable size in bytes that
	// triggers a flush to level 0.
	MemtableLimit int
	// LevelCap is the number of SSTables a level may hold before it is
	// merged into the next level.
	LevelCap int
	// MaxLevels is the total number of levels. Level MaxLevels-1 is the
	// bottom-most level: data merged into it has no older versions
	// underneath, so tombstones are physically dropped there.
	MaxLevels int
}

// DefaultOptions returns a small, test-friendly configuration.
func DefaultOptions() Options {
	return Options{MemtableLimit: 1 << 20, LevelCap: 2, MaxLevels: 4}
}

// DB is a minimal LSM tree with tombstone-based deletion.
type DB struct {
	dir    string
	opts   Options
	mu     sync.RWMutex
	mem    *Memtable
	levels [][]*SSTable
	seq    uint64
	fileID uint64
	closed bool
}

// Open opens (or creates) the database in dir.
func Open(dir string, opts Options) (*DB, error) {
	if opts.MemtableLimit <= 0 {
		opts.MemtableLimit = 1 << 20
	}
	if opts.LevelCap <= 0 {
		opts.LevelCap = 2
	}
	if opts.MaxLevels < 2 {
		opts.MaxLevels = 2
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	db := &DB{
		dir:    dir,
		opts:   opts,
		mem:    NewMemtable(),
		levels: make([][]*SSTable, opts.MaxLevels),
	}
	if err := db.loadSSTables(); err != nil {
		return nil, err
	}
	return db, nil
}

func (db *DB) loadSSTables() error {
	names, err := filepath.Glob(filepath.Join(db.dir, "L*-*.sst"))
	if err != nil {
		return err
	}
	var maxSeq uint64
	for _, name := range names {
		base := filepath.Base(name)
		dash := strings.IndexByte(base, '-')
		dot := strings.LastIndexByte(base, '.')
		if dash < 2 || dot < 0 {
			continue
		}
		level, err := strconv.Atoi(base[1:dash])
		if err != nil || level >= db.opts.MaxLevels {
			continue
		}
		id, err := strconv.ParseUint(base[dash+1:dot], 10, 64)
		if err != nil {
			continue
		}
		tbl, err := LoadSSTable(name, id)
		if err != nil {
			return err
		}
		db.levels[level] = append(db.levels[level], tbl)
		if id > db.fileID {
			db.fileID = id
		}
		for _, e := range tbl.entries {
			if e.Seq > maxSeq {
				maxSeq = e.Seq
			}
		}
	}
	for l := range db.levels {
		sort.Slice(db.levels[l], func(i, j int) bool { return db.levels[l][i].ID < db.levels[l][j].ID })
	}
	db.seq = maxSeq
	return nil
}

// Put inserts or overwrites a key.
func (db *DB) Put(key string, value []byte) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closed {
		return errors.New("lsm: db closed")
	}
	db.seq++
	v := make([]byte, len(value))
	copy(v, value)
	db.mem.Put(Entry{Key: key, Value: v, Seq: db.seq})
	if db.mem.Size() >= db.opts.MemtableLimit {
		return db.flushLocked()
	}
	return nil
}

// Delete removes a key by writing a tombstone marker. The marker shadows
// every older version of the key until compaction physically drops it at
// the bottom-most level.
func (db *DB) Delete(key string) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closed {
		return errors.New("lsm: db closed")
	}
	db.seq++
	db.mem.Put(Entry{Key: key, Tombstone: true, Seq: db.seq})
	if db.mem.Size() >= db.opts.MemtableLimit {
		return db.flushLocked()
	}
	return nil
}

// Get returns the live value for key, or ErrNotFound if the key is absent
// or shadowed by a tombstone.
func (db *DB) Get(key string) ([]byte, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	if e, ok := db.mem.Get(key); ok {
		if e.Tombstone {
			return nil, ErrNotFound
		}
		return e.Value, nil
	}
	for l := 0; l < len(db.levels); l++ {
		// Newest table first within a level.
		for i := len(db.levels[l]) - 1; i >= 0; i-- {
			if e, ok := db.levels[l][i].Get(key); ok {
				if e.Tombstone {
					return nil, ErrNotFound
				}
				return e.Value, nil
			}
		}
	}
	return nil, ErrNotFound
}

// Scan returns all live key/value pairs with start <= key <= end, in key
// order. Tombstoned keys are masked out.
func (db *DB) Scan(start, end string) []KV {
	db.mu.RLock()
	defer db.mu.RUnlock()
	latest := make(map[string]Entry)
	consider := func(e Entry) {
		if e.Key < start || e.Key > end {
			return
		}
		if cur, ok := latest[e.Key]; !ok || e.Seq > cur.Seq {
			latest[e.Key] = e
		}
	}
	for _, e := range db.mem.Entries() {
		consider(e)
	}
	for _, tables := range db.levels {
		for _, t := range tables {
			for _, e := range t.entries {
				consider(e)
			}
		}
	}
	out := make([]KV, 0, len(latest))
	for _, e := range latest {
		if e.Tombstone {
			continue
		}
		out = append(out, KV{Key: e.Key, Value: e.Value})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// Flush forces the current memtable to level 0 and runs compaction.
func (db *DB) Flush() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.flushLocked()
}

func (db *DB) flushLocked() error {
	if len(db.mem.Entries()) == 0 {
		return nil
	}
	db.fileID++
	tbl, err := WriteSSTable(db.tablePath(0, db.fileID), db.fileID, db.mem.Entries())
	if err != nil {
		return err
	}
	db.levels[0] = append(db.levels[0], tbl)
	db.mem.Reset()
	return db.compactLocked()
}

// compactLocked is the lifecycle cleaner: whenever a level overflows its
// table cap, its tables are merged and pushed into the next level. While
// merging, only the newest version of each key survives. When the merge
// target is the bottom-most level, the existing bottom tables are merged
// in as well and tombstone markers are dropped entirely — no older
// version can exist below, so the deletion is final and the space is
// reclaimed.
func (db *DB) compactLocked() error {
	for l := 0; l+1 < db.opts.MaxLevels; l++ {
		for len(db.levels[l]) > db.opts.LevelCap {
			if err := db.mergeIntoLocked(l + 1); err != nil {
				return err
			}
		}
	}
	return nil
}

// mergeIntoLocked merges every table of level target-1 into a single new
// table in level target. At the bottom-most level the tables already
// there are merged in too, so tombstones can be physically dropped.
func (db *DB) mergeIntoLocked(target int) error {
	src := db.levels[target-1]
	bottom := target == db.opts.MaxLevels-1
	var old []*SSTable
	if bottom {
		old = db.levels[target]
	}

	latest := make(map[string]Entry)
	for _, tbl := range src {
		for _, e := range tbl.entries {
			if cur, ok := latest[e.Key]; !ok || e.Seq > cur.Seq {
				latest[e.Key] = e
			}
		}
	}
	for _, tbl := range old {
		for _, e := range tbl.entries {
			if cur, ok := latest[e.Key]; !ok || e.Seq > cur.Seq {
				latest[e.Key] = e
			}
		}
	}

	merged := make([]Entry, 0, len(latest))
	for _, e := range latest {
		if bottom && e.Tombstone {
			// Bottom-most level: the tombstone has fulfilled its
			// purpose. Physically erase it and reclaim the space.
			continue
		}
		merged = append(merged, e)
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].Key < merged[j].Key })

	db.fileID++
	path := db.tablePath(target, db.fileID)
	tbl, err := WriteSSTable(path, db.fileID, merged)
	if err != nil {
		return err
	}
	for _, t := range src {
		t.Remove()
	}
	for _, t := range old {
		t.Remove()
	}
	db.levels[target-1] = nil
	if bottom {
		db.levels[target] = []*SSTable{tbl}
	} else {
		db.levels[target] = append(db.levels[target], tbl)
	}
	return nil
}

func (db *DB) tablePath(level int, id uint64) string {
	return filepath.Join(db.dir, fmt.Sprintf("L%d-%06d.sst", level, id))
}

// LevelStats describes one level for observability and tests.
type LevelStats struct {
	Tables     int
	Entries    int
	Tombstones int
}

func (s LevelStats) String() string {
	return fmt.Sprintf("{tables:%d entries:%d tombstones:%d}", s.Tables, s.Entries, s.Tombstones)
}

// Stats returns per-level statistics.
func (db *DB) Stats() []LevelStats {
	db.mu.RLock()
	defer db.mu.RUnlock()
	stats := make([]LevelStats, len(db.levels))
	for l, tables := range db.levels {
		stats[l].Tables = len(tables)
		for _, t := range tables {
			stats[l].Entries += len(t.entries)
			for _, e := range t.entries {
				if e.Tombstone {
					stats[l].Tombstones++
				}
			}
		}
	}
	return stats
}

// Close flushes the memtable. The DB must not be used afterwards.
func (db *DB) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closed {
		return nil
	}
	db.closed = true
	return db.flushLocked()
}
