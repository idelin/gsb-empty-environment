// Package memtx provides an in-memory transactional key-value store with
// snapshot isolation. Transactions read from a fixed snapshot taken at
// Begin, see their own buffered writes, and commit optimistically with
// write-write conflict detection.
package memtx

import (
	"sort"
	"sync"
)

// Revision identifies a committed version of the database. It increases
// monotonically by one for every successful commit.
type Revision uint64

// versionedValue is one committed version of a key. A nil value is a
// tombstone marking the key as deleted at that revision.
type versionedValue struct {
	rev   Revision
	value []byte
}

// DB is a transactional in-memory key-value store. It is safe for
// concurrent use by multiple goroutines.
type DB struct {
	mu   sync.RWMutex
	rev  Revision
	data map[string][]versionedValue // per key, versions ascending by rev
}

// Open creates an empty database.
func Open() *DB {
	return &DB{data: make(map[string][]versionedValue)}
}

// Revision returns the current committed revision of the database.
func (db *DB) Revision() Revision {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return db.rev
}

// Begin starts a new transaction pinned to the current committed
// revision. The transaction's snapshot never advances: commits made by
// other transactions afterwards are invisible to it.
func (db *DB) Begin() *Txn {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return &Txn{
		db:      db,
		snapRev: db.rev,
		ws:      make(map[string]wsEntry),
	}
}

// lookupLocked returns the value of key visible at snapRev. The caller
// must hold db.mu (read or write). The second return value reports
// whether a live (non-tombstone) version exists at or before snapRev.
func (db *DB) lookupLocked(key string, snapRev Revision) ([]byte, bool) {
	versions := db.data[key]
	// Find the latest version with rev <= snapRev.
	idx := sort.Search(len(versions), func(i int) bool {
		return versions[i].rev > snapRev
	}) - 1
	if idx < 0 || versions[idx].value == nil {
		return nil, false
	}
	return versions[idx].value, true
}

// latestRevLocked returns the newest committed revision of key, or 0 if
// the key was never committed. The caller must hold db.mu.
func (db *DB) latestRevLocked(key string) Revision {
	versions := db.data[key]
	if len(versions) == 0 {
		return 0
	}
	return versions[len(versions)-1].rev
}

// commitLocked validates the write set against concurrent commits and,
// if clean, applies every entry at a single new revision. The caller
// must hold db.mu for writing.
func (db *DB) commitLocked(tx *Txn) error {
	for key := range tx.ws {
		if db.latestRevLocked(key) > tx.snapRev {
			return ErrConflict
		}
	}
	db.rev++
	for key, entry := range tx.ws {
		var value []byte
		if !entry.deleted {
			value = entry.value
		}
		db.data[key] = append(db.data[key], versionedValue{rev: db.rev, value: value})
	}
	return nil
}
