package bptree

// Iterator performs an ordered range scan over the leaf chain.
// It holds exactly one pinned leaf page at a time: crossing a page
// boundary pins the next leaf before unpinning the current one.
type Iterator struct {
	pool   *BufferPool
	end    int // exclusive upper bound
	page   *Page
	idx    int
	closed bool
	err    error
}

// Scan returns an iterator over the half-open range [start, end).
// The caller must call Close (Next closes automatically at the end).
func (t *BPlusTree) Scan(start, end int) (*Iterator, error) {
	leaf, err := t.findLeaf(start)
	if err != nil {
		return nil, err
	}
	return &Iterator{
		pool: t.pool,
		end:  end,
		page: leaf,
		idx:  lowerBound(leaf.node.keys, start),
	}, nil
}

// Next returns the next (key, value) pair in key order.
// ok is false once the range is exhausted or an error occurred;
// check Err to distinguish the two.
func (it *Iterator) Next() (key, val int, ok bool) {
	if it.closed {
		return 0, 0, false
	}
	for {
		n := it.page.node
		if it.idx < len(n.keys) {
			k := n.keys[it.idx]
			if k >= it.end {
				it.Close()
				return 0, 0, false
			}
			v := n.vals[it.idx]
			it.idx++
			return k, v, true
		}
		// Page boundary: pin the next leaf, then unpin the old one.
		next := n.next
		if next == InvalidPageID {
			it.Close()
			return 0, 0, false
		}
		np, err := it.pool.FetchPage(next)
		if err != nil {
			it.err = err
			it.Close()
			return 0, 0, false
		}
		if err := it.pool.UnpinPage(it.page.id, false); err != nil {
			it.err = err
			it.Close()
			return 0, 0, false
		}
		it.page = np
		it.idx = 0
	}
}

// Err reports the first error encountered during iteration, if any.
func (it *Iterator) Err() error { return it.err }

// Close unpins the current leaf. It is safe to call more than once.
func (it *Iterator) Close() {
	if it.closed {
		return
	}
	it.closed = true
	if it.page != nil {
		if err := it.pool.UnpinPage(it.page.id, false); err != nil && it.err == nil {
			it.err = err
		}
		it.page = nil
	}
}
