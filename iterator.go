package bptree

import "sort"

// Iterator performs an ordered range scan over the half-open interval
// [lo, hi). It holds exactly one leaf page pinned at a time; when the
// scan crosses a physical page boundary it pins the next leaf via the
// sibling chain and unpins the old one.
type Iterator struct {
	pool *BufferPool
	hi   int64
	leaf *Page // currently pinned leaf, nil once released
	idx  int   // next slot inside leaf
	done bool
	err  error
}

// Scan locates the leaf and slot for lo, then returns an iterator over
// [lo, hi). The caller must call Close if the loop exits early; a loop
// that runs until Next returns ok == false closes the iterator itself.
func (t *BPlusTree) Scan(lo, hi int64) (*Iterator, error) {
	leaf, err := t.findLeaf(lo)
	if err != nil {
		return nil, err
	}
	idx := sort.Search(len(leaf.node.keys), func(i int) bool { return leaf.node.keys[i] >= lo })
	return &Iterator{pool: t.pool, hi: hi, leaf: leaf, idx: idx}, nil
}

// Next returns the next (key, value) in range, or ok == false when the
// scan is exhausted or hi is reached.
func (it *Iterator) Next() (key, val int64, ok bool) {
	for !it.done {
		n := it.leaf.node
		if it.idx < len(n.keys) {
			k := n.keys[it.idx]
			if k >= it.hi {
				it.Close()
				break
			}
			v := n.vals[it.idx]
			it.idx++
			return k, v, true
		}
		// Page boundary crossing: hop to the right sibling.
		next := n.next
		it.pool.Unpin(it.leaf.ID)
		it.leaf = nil
		if next == InvalidPageID {
			it.done = true
			break
		}
		pg, err := it.pool.Fetch(next)
		if err != nil {
			it.err = err
			it.done = true
			break
		}
		it.leaf = pg
		it.idx = 0
	}
	return 0, 0, false
}

// Close unpins the current leaf. It is idempotent and safe to call after
// the iterator has already finished.
func (it *Iterator) Close() {
	if it.leaf != nil {
		it.pool.Unpin(it.leaf.ID)
		it.leaf = nil
	}
	it.done = true
}

// Err reports a buffer-pool failure encountered during iteration.
func (it *Iterator) Err() error { return it.err }
