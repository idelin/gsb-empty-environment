package cowtree

import (
	"sync"
	"sync/atomic"
)

// DB is a CoW B+ tree database. Writers are serialized by a mutex; readers
// never take any lock.
type DB struct {
	wmu   sync.Mutex // serializes write transactions
	next  atomic.Uint64
	root  atomic.Pointer[page]
	pages atomic.Uint64 // total pages ever allocated (observability)
}

// Open returns an empty database.
func Open() *DB { return &DB{} }

func (db *DB) allocID() uint64 {
	db.pages.Add(1)
	return db.next.Add(1)
}

// PagesAllocated reports how many pages have been allocated in total.
// CoW means every write allocates new pages instead of touching old ones.
func (db *DB) PagesAllocated() uint64 { return db.pages.Load() }

// RootPageID returns the page ID of the current root (0 if empty).
func (db *DB) RootPageID() uint64 {
	if r := db.root.Load(); r != nil {
		return r.id
	}
	return 0
}

// Put inserts or updates key in a single write transaction. The write path
// copies every page from the root down to the target leaf and swaps the
// root atomically; no existing page is ever modified.
func (db *DB) Put(key, val int64) {
	db.wmu.Lock()
	defer db.wmu.Unlock()

	oldRoot := db.root.Load()
	if oldRoot == nil {
		db.root.Store(&page{id: db.allocID(), leaf: true, keys: []int64{key}, vals: []int64{val}})
		return
	}
	newRoot, sp := insert(oldRoot, key, val, db)
	if sp != nil {
		newRoot = &page{
			id:       db.allocID(),
			keys:     []int64{sp.sep},
			children: []*page{newRoot, sp.right},
		}
	}
	db.root.Store(newRoot)
}

// Get reads the latest committed version of key.
func (db *DB) Get(key int64) (int64, bool) {
	return get(db.root.Load(), key)
}

// View opens a read-only transaction: a stable, lock-free snapshot of the
// table. The snapshot stays valid and consistent no matter what writers do
// afterwards, because the pages it references are immutable.
func (db *DB) View() *Snapshot {
	return &Snapshot{root: db.root.Load()}
}

// Snapshot is a read-only transaction bound to one immutable root.
type Snapshot struct {
	root *page
}

// RootPageID identifies the snapshot's root page.
func (s *Snapshot) RootPageID() uint64 {
	if s.root == nil {
		return 0
	}
	return s.root.id
}

// Get looks up key in the snapshot.
func (s *Snapshot) Get(key int64) (int64, bool) {
	return get(s.root, key)
}

// Iterator returns a cursor that scans the whole snapshot in key order via
// an in-order traversal of the immutable pages.
func (s *Snapshot) Iterator() *Iterator {
	it := &Iterator{}
	if s.root != nil {
		it.stack = []frame{{p: s.root}}
	}
	return it
}

// frame tracks how far an internal page has been traversed.
type frame struct {
	p     *page
	child int // next child index to descend into
}

// Iterator scans a snapshot in ascending key order. It holds only a stack
// of immutable pages, so it needs no locks and never observes a write.
type Iterator struct {
	stack []frame
	leaf  *page
	idx   int // 1-based: current entry is leaf.keys[idx-1]
}

// Next advances the cursor. It returns false when the scan is done.
func (it *Iterator) Next() bool {
	for {
		if it.leaf != nil {
			if it.idx < len(it.leaf.keys) {
				it.idx++
				return true
			}
			it.leaf = nil
			it.idx = 0
		}
		found := false
		for len(it.stack) > 0 {
			top := &it.stack[len(it.stack)-1]
			if top.child >= len(top.p.children) {
				it.stack = it.stack[:len(it.stack)-1]
				continue
			}
			child := top.p.children[top.child]
			top.child++
			if child.leaf {
				it.leaf = child
				found = true
				break
			}
			it.stack = append(it.stack, frame{p: child})
		}
		if !found {
			return false
		}
	}
}

// Key returns the current key. Call after Next returned true.
func (it *Iterator) Key() int64 { return it.leaf.keys[it.idx-1] }

// Value returns the current value. Call after Next returned true.
func (it *Iterator) Value() int64 { return it.leaf.vals[it.idx-1] }
