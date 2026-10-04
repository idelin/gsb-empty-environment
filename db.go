package bptree

import (
	"fmt"
	"sync"
)

// DB is a copy-on-write B+ tree over a page file. Writers serialize on a
// single mutex; readers (snapshots, iterators, Get) never take the lock
// because committed pages are immutable.
type DB struct {
	mu       sync.Mutex
	p        *pager
	capacity int
	minKeys  int
	closed   bool

	// snapRoots counts active snapshots per root page id. Pages reachable
	// from any of these roots (plus the current root) are never reclaimed.
	snapRoots map[uint64]int
}

// Open opens or creates the page file at path. capacity (max keys per node)
// is used when creating a new file; pass 0 to reuse the stored capacity of
// an existing file.
func Open(path string, capacity int) (*DB, error) {
	p, err := openPager(path, capacity)
	if err != nil {
		return nil, err
	}
	minKeys := p.capacity / 2
	if minKeys < 1 {
		minKeys = 1
	}
	return &DB{
		p:         p,
		capacity:  p.capacity,
		minKeys:   minKeys,
		snapRoots: map[uint64]int{},
	}, nil
}

// Close flushes and closes the page file.
func (db *DB) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closed {
		return nil
	}
	db.closed = true
	return db.p.close()
}

// Root returns the current root page id.
func (db *DB) Root() uint64 {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.p.root
}

// Seq returns the current commit sequence number.
func (db *DB) Seq() uint64 {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.p.seq
}

// Update runs fn as a single write transaction. If fn returns an error or
// any page write fails, the transaction is rolled back and no new root is
// published. On success a new root becomes visible atomically.
func (db *DB) Update(fn func(tx *Tx) error) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closed {
		return ErrClosed
	}
	t := &txState{
		db:     db,
		seq:    db.p.seq + 1,
		root:   db.p.root,
		dirty:  map[uint64]*node{},
		copied: map[uint64]uint64{},
	}
	if err := fn(&Tx{t: t}); err != nil {
		t.rollback()
		return err
	}
	if err := t.fixLeafLinks(); err != nil {
		t.rollback()
		return err
	}
	// Commit protocol: write all dirty data pages first, then the meta page
	// (which publishes the new root), then sync. Any failure before the meta
	// write leaves the previous root untouched.
	for _, n := range t.dirty {
		if err := db.p.writeNode(n); err != nil {
			t.rollback()
			return fmt.Errorf("bptree: commit: %w", err)
		}
	}
	db.p.root = t.root
	db.p.seq = t.seq
	if err := db.p.writeMeta(); err != nil {
		t.rollback()
		return fmt.Errorf("bptree: commit: %w", err)
	}
	if err := db.p.sync(); err != nil {
		return fmt.Errorf("bptree: commit: %w", err)
	}
	return nil
}

// getFrom descends the tree rooted at root without any locking; committed
// pages are immutable so this is race-free with concurrent commits.
func (db *DB) getFrom(root uint64, key int64) ([]byte, error) {
	id := root
	for {
		n, err := db.p.readNode(id)
		if err != nil {
			return nil, err
		}
		if n.leaf {
			i := sortSearchGE(n.keys, key)
			if i < len(n.keys) && n.keys[i] == key {
				return append([]byte(nil), n.values[i]...), nil
			}
			return nil, ErrNotFound
		}
		id = n.child[sortSearchLT(n.keys, key)]
	}
}

// Get reads a key from the current version.
func (db *DB) Get(key int64) ([]byte, error) {
	db.mu.Lock()
	if db.closed {
		db.mu.Unlock()
		return nil, ErrClosed
	}
	root := db.p.root
	db.mu.Unlock()
	return db.getFrom(root, key)
}

// Snapshot pins the current root and returns a read-only view of that
// version. The pinned version stays readable until Close is called, no
// matter how many write transactions commit in between.
func (db *DB) Snapshot() *Snapshot {
	db.mu.Lock()
	defer db.mu.Unlock()
	s := &Snapshot{db: db, root: db.p.root, seq: db.p.seq}
	db.snapRoots[s.root]++
	return s
}

// Reclaim frees every page that is unreachable from the current root and
// from all roots pinned by open snapshots. It returns the number of freed
// pages. Freed page ids are reused by later commits.
func (db *DB) Reclaim() (int, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closed {
		return 0, ErrClosed
	}
	reachable := map[uint64]bool{0: true} // meta page
	roots := make([]uint64, 0, len(db.snapRoots)+1)
	roots = append(roots, db.p.root)
	for r := range db.snapRoots {
		roots = append(roots, r)
	}
	queue := append([]uint64(nil), roots...)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if reachable[id] {
			continue
		}
		reachable[id] = true
		n, err := db.p.readNode(id)
		if err != nil {
			return 0, fmt.Errorf("bptree: reclaim: %w", err)
		}
		if !n.leaf {
			queue = append(queue, n.child...)
		}
	}
	freed := 0
	for id := uint64(1); id < db.p.numPages; id++ {
		if db.p.freeSet[id] || reachable[id] {
			continue
		}
		if err := db.p.free(id); err != nil {
			return freed, err
		}
		freed++
	}
	return freed, nil
}

// CheckIntegrity verifies the meta page and the magic, type and checksum of
// every allocated page. It reports the first corrupt page found.
func (db *DB) CheckIntegrity() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if err := db.p.readMeta(); err != nil {
		return err
	}
	for id := uint64(1); id < db.p.numPages; id++ {
		if db.p.freeSet[id] {
			continue
		}
		if _, err := db.p.readNode(id); err != nil {
			return fmt.Errorf("bptree: integrity check failed: %w", err)
		}
	}
	return nil
}

func sortSearchGE(keys []int64, key int64) int {
	lo, hi := 0, len(keys)
	for lo < hi {
		mid := (lo + hi) / 2
		if keys[mid] < key {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

func sortSearchLT(keys []int64, key int64) int {
	lo, hi := 0, len(keys)
	for lo < hi {
		mid := (lo + hi) / 2
		if key < keys[mid] {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return lo
}
