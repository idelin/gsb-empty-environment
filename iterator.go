package bptree

import (
	"sort"
	"sync"
)

// Snapshot is a read handle pinned to a fixed root version. It is safe for
// concurrent use. After Close, every operation fails with ErrSnapshotClosed
// and its pages become eligible for reclamation.
type Snapshot struct {
	tree    *Tree
	root    PageID
	version uint64

	mu     sync.RWMutex
	closed bool
}

// Version returns the commit sequence this snapshot is pinned to.
func (s *Snapshot) Version() uint64 { return s.version }

// Root returns the pinned root page id, e.g. for independent validation.
func (s *Snapshot) Root() PageID { return s.root }

// Close releases the snapshot. Iterators derived from it start failing with
// ErrSnapshotClosed.
func (s *Snapshot) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()

	t := s.tree
	t.mu.Lock()
	if t.activeRoots[s.root] <= 1 {
		delete(t.activeRoots, s.root)
	} else {
		t.activeRoots[s.root]--
	}
	t.mu.Unlock()
}

func (s *Snapshot) loadNode(id PageID) (*node, error) {
	buf, err := s.tree.store.Read(id)
	if err != nil {
		return nil, err
	}
	return decodeNode(buf)
}

// Get reads a key from the snapshot's fixed version.
func (s *Snapshot) Get(key int64) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, ErrSnapshotClosed
	}
	cur, err := s.loadNode(s.root)
	if err != nil {
		return nil, err
	}
	for !cur.leaf {
		i := sort.Search(len(cur.keys), func(i int) bool { return key < cur.keys[i] })
		if cur, err = s.loadNode(cur.children[i]); err != nil {
			return nil, err
		}
	}
	i := sort.Search(len(cur.keys), func(i int) bool { return cur.keys[i] >= key })
	if i < len(cur.keys) && cur.keys[i] == key {
		return append([]byte(nil), cur.vals[i]...), nil
	}
	return nil, ErrNotFound
}

// Iterator returns a range iterator over [start, end). An empty range
// (start >= end) yields an immediately exhausted iterator.
func (s *Snapshot) Iterator(start, end int64) *Iterator {
	it := &Iterator{snap: s, end: end}
	if start >= end {
		it.done = true
		return it
	}
	if err := it.position(start); err != nil {
		it.err = err
		it.done = true
	}
	return it
}

// Iterator walks a key range of one fixed snapshot version.
//
// It keeps the decoded path from the pinned root to the current leaf and
// moves between leaves by ascending to the parent and descending into the
// next child. Every page it touches is reachable from the snapshot's root,
// so it can never follow a link into a newer (or recycled) version of the
// tree: there are no leaf sibling links to follow.
type Iterator struct {
	snap *Snapshot
	end  int64

	path []*node // internal nodes from root to the leaf's parent
	pidx []int   // child index taken at each path level
	leaf *node
	idx  int

	key int64
	val []byte

	err  error
	done bool
}

func (it *Iterator) position(start int64) error {
	s := it.snap
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return ErrSnapshotClosed
	}
	cur, err := s.loadNode(s.root)
	if err != nil {
		return err
	}
	for !cur.leaf {
		i := sort.Search(len(cur.keys), func(i int) bool { return start < cur.keys[i] })
		child, err := s.loadNode(cur.children[i])
		if err != nil {
			return err
		}
		it.path = append(it.path, cur)
		it.pidx = append(it.pidx, i)
		cur = child
	}
	it.leaf = cur
	it.idx = sort.Search(len(cur.keys), func(i int) bool { return cur.keys[i] >= start })
	return nil
}

// advanceLeaf moves to the next leaf of the pinned version by walking the
// path stack. It returns false when the tree is exhausted.
func (it *Iterator) advanceLeaf() bool {
	s := it.snap
	for len(it.path) > 0 {
		top := len(it.path) - 1
		parent := it.path[top]
		it.pidx[top]++
		if it.pidx[top] >= len(parent.children) {
			it.path = it.path[:top]
			it.pidx = it.pidx[:top]
			continue
		}
		cur, err := s.loadNode(parent.children[it.pidx[top]])
		if err != nil {
			it.err = err
			return false
		}
		for !cur.leaf {
			it.path = append(it.path, cur)
			it.pidx = append(it.pidx, 0)
			if cur, err = s.loadNode(cur.children[0]); err != nil {
				it.err = err
				return false
			}
		}
		it.leaf = cur
		it.idx = 0
		return true
	}
	return false
}

// Next advances the iterator. It returns false on exhaustion, close, or
// error; inspect Err to distinguish.
func (it *Iterator) Next() bool {
	if it.err != nil || it.done {
		return false
	}
	s := it.snap
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		it.err = ErrSnapshotClosed
		return false
	}
	for {
		if it.leaf != nil && it.idx < len(it.leaf.keys) {
			k := it.leaf.keys[it.idx]
			if k >= it.end {
				it.done = true
				return false
			}
			it.key = k
			it.val = append([]byte(nil), it.leaf.vals[it.idx]...)
			it.idx++
			return true
		}
		if !it.advanceLeaf() {
			it.done = true
			return false
		}
	}
}

// Key returns the current key. Valid only after a successful Next.
func (it *Iterator) Key() int64 { return it.key }

// Value returns a copy of the current value. Valid only after a successful Next.
func (it *Iterator) Value() []byte { return append([]byte(nil), it.val...) }

// Err returns the first error encountered, or nil on clean exhaustion.
func (it *Iterator) Err() error { return it.err }

// Close releases the iterator. Further Next calls return false.
func (it *Iterator) Close() {
	it.done = true
	it.path = nil
	it.pidx = nil
	it.leaf = nil
	it.val = nil
}
