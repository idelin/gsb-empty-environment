package pageindex

import (
	"math"
	"sync"
)

// KV is one key/value pair in key order.
type KV struct {
	Key uint64
	Val uint64
}

type snapshotRef struct {
	root  PageNo
	count int
}

// DB is an immutable, copy-on-write B-tree index over explicitly numbered
// pages. Writers commit new roots; snapshots pin the root of their epoch and
// keep every page they can reach alive. Commits are serialized; reads and
// snapshot traversal are lock-free with respect to each other.
type DB struct {
	mu    sync.Mutex
	pager *Pager
	t     int
	root  PageNo
	epoch uint64
	snaps map[uint64]snapshotRef // epoch -> pinned root + active count
}

// New opens an empty index with minimum degree t (nodes hold t-1..2t-1 keys).
func New(t int) *DB {
	if t < 2 {
		t = 2
	}
	p := NewPager()
	root := p.alloc(&node{leaf: true})
	return &DB{pager: p, t: t, root: root, snaps: make(map[uint64]snapshotRef)}
}

// Root returns the current root page number.
func (db *DB) Root() PageNo {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.root
}

// Epoch returns the current commit epoch.
func (db *DB) Epoch() uint64 {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.epoch
}

// Get reads the current root.
func (db *DB) Get(key uint64) (uint64, bool) {
	db.mu.Lock()
	root := db.root
	db.mu.Unlock()
	return getFrom(db.pager, root, key)
}

// Items returns all key/value pairs of the current root in key order.
func (db *DB) Items() []KV {
	db.mu.Lock()
	root := db.root
	db.mu.Unlock()
	return collectItems(db.pager, root)
}

// Put inserts or replaces key and commits a new root.
func (db *DB) Put(key, val uint64) {
	db.mu.Lock()
	defer db.mu.Unlock()
	c := &cow{p: db.pager, t: db.t}
	newRoot := c.insert(db.root, key, val)
	db.commitLocked(newRoot, c.retired)
}

// Delete removes key and commits a new root. It reports whether key existed.
func (db *DB) Delete(key uint64) bool {
	db.mu.Lock()
	defer db.mu.Unlock()
	if _, ok := getFrom(db.pager, db.root, key); !ok {
		return false
	}
	c := &cow{p: db.pager, t: db.t}
	newRoot := c.delete(db.root, key)
	db.commitLocked(newRoot, c.retired)
	return true
}

// Snapshot pins the current root and epoch until Close is called.
func (db *DB) Snapshot() *Snapshot {
	db.mu.Lock()
	defer db.mu.Unlock()
	ref := db.snaps[db.epoch]
	ref.root = db.root
	ref.count++
	db.snaps[db.epoch] = ref
	return &Snapshot{db: db, root: db.root, epoch: db.epoch}
}

// release unregisters one snapshot at epoch and runs reclamation.
func (db *DB) release(epoch uint64) {
	db.mu.Lock()
	defer db.mu.Unlock()
	ref, ok := db.snaps[epoch]
	if !ok {
		panic("pageindex: snapshot released twice")
	}
	ref.count--
	if ref.count == 0 {
		delete(db.snaps, epoch)
	} else {
		db.snaps[epoch] = ref
	}
	db.gcLocked()
}

func (db *DB) commitLocked(root PageNo, retired []PageNo) {
	db.epoch++
	db.root = root
	for _, no := range retired {
		db.pager.retire(no, db.epoch)
	}
	db.gcLocked()
}

// gcLocked frees every retired page no active snapshot can still reach.
func (db *DB) gcLocked() {
	min, ok := db.minActiveEpochLocked()
	db.pager.reclaim(min, ok)
}

func (db *DB) minActiveEpochLocked() (uint64, bool) {
	if len(db.snaps) == 0 {
		return 0, false
	}
	min := uint64(math.MaxUint64)
	for e := range db.snaps {
		if e < min {
			min = e
		}
	}
	return min, true
}

func getFrom(p *Pager, root PageNo, key uint64) (uint64, bool) {
	n := p.get(root)
	for {
		i := n.find(key)
		if i < len(n.keys) && n.keys[i] == key {
			return n.vals[i], true
		}
		if n.leaf {
			return 0, false
		}
		n = p.get(n.kids[i])
	}
}

func collectItems(p *Pager, root PageNo) []KV {
	var out []KV
	var walk func(no PageNo)
	walk = func(no PageNo) {
		n := p.get(no)
		for i := range n.keys {
			if !n.leaf {
				walk(n.kids[i])
			}
			out = append(out, KV{Key: n.keys[i], Val: n.vals[i]})
		}
		if !n.leaf {
			walk(n.kids[len(n.keys)])
		}
	}
	walk(root)
	return out
}
