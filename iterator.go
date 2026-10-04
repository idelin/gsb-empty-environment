package bptree

import (
	"sync/atomic"
)

// Snapshot is a pinned, read-only view of one committed version.
type Snapshot struct {
	db     *DB
	root   uint64
	seq    uint64
	closed atomic.Int32
}

// Close releases the pinned root so its pages become reclaimable. Iterators
// created from this snapshot keep working until the snapshot is closed;
// after Close every iterator operation fails with ErrSnapshotClosed.
func (s *Snapshot) Close() error {
	if s.closed.CompareAndSwap(0, 1) {
		s.db.mu.Lock()
		if s.db.snapRoots[s.root] <= 1 {
			delete(s.db.snapRoots, s.root)
		} else {
			s.db.snapRoots[s.root]--
		}
		s.db.mu.Unlock()
	}
	return nil
}

func (s *Snapshot) isClosed() bool { return s.closed.Load() != 0 }

// Get reads a key from the pinned version.
func (s *Snapshot) Get(key int64) ([]byte, error) {
	if s.isClosed() {
		return nil, ErrSnapshotClosed
	}
	return s.db.getFrom(s.root, key)
}

// Scan returns an iterator over the half-open range [start, end). An empty
// range (start >= end) yields no items.
func (s *Snapshot) Scan(start, end int64) *Iterator {
	return &Iterator{snap: s, start: start, end: end}
}

// Root returns the pinned root page id (for use with Validate).
func (s *Snapshot) Root() uint64 { return s.root }

// pathEntry records which child was taken at an internal node.
type pathEntry struct {
	id    uint64
	index int
}

// descend finds the leaf that would hold key, returning the internal-node
// path, the leaf, and the lower-bound index of key inside the leaf.
func (db *DB) descend(root uint64, key int64) ([]pathEntry, *node, int, error) {
	var path []pathEntry
	id := root
	for {
		n, err := db.p.readNode(id)
		if err != nil {
			return nil, nil, 0, err
		}
		if n.leaf {
			return path, n, sortSearchGE(n.keys, key), nil
		}
		i := sortSearchLT(n.keys, key)
		path = append(path, pathEntry{id: id, index: i})
		id = n.child[i]
	}
}

// nextLeaf returns the leaf following the one addressed by path, or nil if
// the path addressed the last leaf. Navigation uses only child pointers of
// the pinned tree, never leaf links.
func (db *DB) nextLeaf(path []pathEntry) ([]pathEntry, *node, error) {
	for len(path) > 0 {
		top := path[len(path)-1]
		path = path[:len(path)-1]
		n, err := db.p.readNode(top.id)
		if err != nil {
			return nil, nil, err
		}
		if top.index+1 >= len(n.child) {
			continue
		}
		path = append(path, pathEntry{id: top.id, index: top.index + 1})
		id := n.child[top.index+1]
		for {
			cn, err := db.p.readNode(id)
			if err != nil {
				return nil, nil, err
			}
			if cn.leaf {
				return path, cn, nil
			}
			path = append(path, pathEntry{id: id, index: 0})
			id = cn.child[0]
		}
	}
	return nil, nil, nil
}

// seekGT positions on the first key strictly greater than last.
func (s *Snapshot) seekGT(last int64) ([]pathEntry, *node, int, error) {
	path, leaf, idx, err := s.db.descend(s.root, last)
	if err != nil {
		return nil, nil, 0, err
	}
	for {
		for idx < len(leaf.keys) && leaf.keys[idx] <= last {
			idx++
		}
		if idx < len(leaf.keys) {
			return path, leaf, idx, nil
		}
		path, leaf, err = s.db.nextLeaf(path)
		if err != nil || leaf == nil {
			return nil, nil, 0, err
		}
		idx = 0
	}
}

// Iterator walks a half-open key range of one pinned snapshot version.
//
// Leaf pages carry next links, but a link is only trustworthy inside the
// commit that created it: a later commit copies modified leaves to new
// pages and never updates the old page's link. The iterator therefore
// follows a leaf link only when the current leaf belongs to the snapshot's
// own commit (version == snapshot seq); otherwise it re-seeks inside the
// pinned tree by key. This guarantees an old reader can never follow a
// newer version's link and jump into the new tree.
type Iterator struct {
	snap    *Snapshot
	start   int64
	end     int64
	leaf    *node
	idx     int
	path    []pathEntry
	last    int64
	started bool
	done    bool
	err     error
}

// Next advances the iterator. It returns false at the end of the range or
// on error; check Err afterwards.
func (it *Iterator) Next() bool {
	if it.err != nil {
		return false
	}
	if it.snap.isClosed() {
		it.err = ErrSnapshotClosed
		return false
	}
	if it.done {
		return false
	}
	if !it.started {
		it.started = true
		if it.start >= it.end {
			it.done = true
			return false
		}
		path, leaf, idx, err := it.snap.db.descend(it.snap.root, it.start)
		if err != nil {
			it.err = err
			return false
		}
		for idx >= len(leaf.keys) {
			path, leaf, err = it.snap.db.nextLeaf(path)
			if err != nil {
				it.err = err
				return false
			}
			if leaf == nil {
				it.done = true
				return false
			}
			idx = 0
		}
		it.path, it.leaf, it.idx = path, leaf, idx
	} else {
		it.idx++
		for it.idx >= len(it.leaf.keys) {
			if it.leaf.version == it.snap.seq {
				// Link written by the snapshot's own commit: safe to follow.
				if it.leaf.next == 0 {
					it.done = true
					return false
				}
				n, err := it.snap.db.p.readNode(it.leaf.next)
				if err != nil {
					it.err = err
					return false
				}
				if !n.leaf || n.version > it.snap.seq {
					it.err = ErrCorrupt
					return false
				}
				it.leaf = n
				it.idx = 0
				it.path = nil
			} else {
				// Older shared leaf: its link may point at a stale page.
				// Re-seek by key inside the pinned tree instead.
				path, leaf, idx, err := it.snap.seekGT(it.last)
				if err != nil {
					it.err = err
					return false
				}
				if leaf == nil {
					it.done = true
					return false
				}
				it.path, it.leaf, it.idx = path, leaf, idx
			}
		}
	}
	k := it.leaf.keys[it.idx]
	if k >= it.end {
		it.done = true
		return false
	}
	it.last = k
	return true
}

// Key returns the current key. Valid only after a successful Next.
func (it *Iterator) Key() int64 { return it.leaf.keys[it.idx] }

// Value returns a copy of the current value.
func (it *Iterator) Value() []byte { return append([]byte(nil), it.leaf.values[it.idx]...) }

// Err returns the first error encountered, if any.
func (it *Iterator) Err() error { return it.err }
