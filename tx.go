package bptree

import "sort"

// Tx is a copy-on-write write transaction. At most one Tx is open at a time.
// All modifications are applied to freshly allocated pages; the previous root
// stays untouched and readable until Commit publishes the new root. If Commit
// fails (e.g. a page write error), nothing is published and the tree keeps
// serving the old root.
type Tx struct {
	tree  *Tree
	root  PageID
	dirty map[PageID]*node // new pages created by this transaction
	done  bool
}

// Begin opens a write transaction, blocking until any previous one finishes.
func (t *Tree) Begin() *Tx {
	t.txMu.Lock()
	t.mu.RLock()
	root := t.root
	t.mu.RUnlock()
	return &Tx{tree: t, root: root, dirty: make(map[PageID]*node)}
}

// load resolves a page id to a node, preferring pages created by this tx.
func (tx *Tx) load(id PageID) (*node, error) {
	if n, ok := tx.dirty[id]; ok {
		return n, nil
	}
	buf, err := tx.tree.store.Read(id)
	if err != nil {
		return nil, err
	}
	return decodeNode(buf)
}

// cow returns a writable private copy of the node, allocated on a new page.
// The original page is never modified.
func (tx *Tx) cow(id PageID) (*node, error) {
	if n, ok := tx.dirty[id]; ok {
		return n, nil
	}
	n, err := tx.load(id)
	if err != nil {
		return nil, err
	}
	cp := &node{id: tx.tree.store.Alloc(), leaf: n.leaf}
	cp.keys = append([]int64(nil), n.keys...)
	if n.leaf {
		cp.vals = make([][]byte, len(n.vals))
		for i, v := range n.vals {
			cp.vals[i] = append([]byte(nil), v...)
		}
	} else {
		cp.children = append([]PageID(nil), n.children...)
	}
	tx.dirty[cp.id] = cp
	return cp, nil
}

// repairPath re-descends to key and restores the exact separator invariant
// (separator == minimum key of the subtree to its right) along the path.
// Splits, borrows and merges maintain separators locally; this pass repairs
// the cases they cannot see, e.g. a leaf's minimum moving across an ancestor
// boundary after its first key was deleted or a smaller key was inserted.
func (tx *Tx) repairPath(key int64) error {
	path, _, err := tx.descend(key)
	if err != nil {
		return err
	}
	for j := len(path) - 1; j >= 0; j-- {
		parent, idx := path[j].n, path[j].idx
		if idx == 0 {
			continue
		}
		m, err := tx.subtreeMin(parent.children[idx])
		if err != nil {
			return err
		}
		if parent.keys[idx-1] != m {
			parent.keys[idx-1] = m
		}
	}
	return nil
}

// subtreeMin returns the smallest key of the subtree rooted at id.
func (tx *Tx) subtreeMin(id PageID) (int64, error) {
	n, err := tx.load(id)
	if err != nil {
		return 0, err
	}
	for !n.leaf {
		if n, err = tx.load(n.children[0]); err != nil {
			return 0, err
		}
	}
	if len(n.keys) == 0 {
		return 0, ErrCorrupt
	}
	return n.keys[0], nil
}

type frame struct {
	n   *node // cow-copied parent
	idx int   // index of the child we descended into
}

// descend walks from the tx root to the leaf holding key, cow-copying every
// node on the path and returning the path (excluding the leaf) and the leaf.
func (tx *Tx) descend(key int64) ([]frame, *node, error) {
	root, err := tx.cow(tx.root)
	if err != nil {
		return nil, nil, err
	}
	tx.root = root.id
	var path []frame
	cur := root
	for !cur.leaf {
		i := sort.Search(len(cur.keys), func(i int) bool { return key < cur.keys[i] })
		child, err := tx.cow(cur.children[i])
		if err != nil {
			return nil, nil, err
		}
		cur.children[i] = child.id
		path = append(path, frame{cur, i})
		cur = child
	}
	return path, cur, nil
}

// Put inserts or overwrites a key. Duplicate keys replace the stored value.
func (tx *Tx) Put(key int64, value []byte) error {
	if tx.done {
		return ErrTxDone
	}
	if len(value) > tx.tree.maxVal {
		return ErrValueTooLarge
	}
	path, leaf, err := tx.descend(key)
	if err != nil {
		return err
	}
	i := sort.Search(len(leaf.keys), func(i int) bool { return leaf.keys[i] >= key })
	if i < len(leaf.keys) && leaf.keys[i] == key {
		leaf.vals[i] = append([]byte(nil), value...)
		return nil
	}
	leaf.keys = insertInt64(leaf.keys, i, key)
	leaf.vals = insertBytes(leaf.vals, i, append([]byte(nil), value...))
	if err := tx.split(path, leaf); err != nil {
		return err
	}
	return tx.repairPath(key)
}

// split propagates node splits from cur up the path to the root.
func (tx *Tx) split(path []frame, cur *node) error {
	cap := tx.tree.capacity
	for len(cur.keys) > cap {
		var sep int64
		var right *node
		if cur.leaf {
			mid := len(cur.keys) / 2
			right = &node{
				id:   tx.tree.store.Alloc(),
				leaf: true,
				keys: append([]int64(nil), cur.keys[mid:]...),
				vals: copyBytes(cur.vals[mid:]),
			}
			cur.keys = cur.keys[:mid]
			cur.vals = cur.vals[:mid]
			sep = right.keys[0]
		} else {
			mid := (len(cur.keys) - 1) / 2
			sep = cur.keys[mid]
			right = &node{
				id:       tx.tree.store.Alloc(),
				keys:     append([]int64(nil), cur.keys[mid+1:]...),
				children: append([]PageID(nil), cur.children[mid+1:]...),
			}
			cur.keys = cur.keys[:mid]
			cur.children = cur.children[:mid+1]
		}
		tx.dirty[right.id] = right

		if len(path) == 0 {
			// The root split: grow the tree one level.
			nr := &node{
				id:       tx.tree.store.Alloc(),
				keys:     []int64{sep},
				children: []PageID{cur.id, right.id},
			}
			tx.dirty[nr.id] = nr
			tx.root = nr.id
			return nil
		}
		f := path[len(path)-1]
		path = path[:len(path)-1]
		parent := f.n
		parent.keys = insertInt64(parent.keys, f.idx, sep)
		parent.children = insertPageID(parent.children, f.idx+1, right.id)
		cur = parent
	}
	return nil
}

// Delete removes a key. It returns ErrNotFound if the key is absent.
// Underflowing nodes are repaired by borrowing from a sibling or merging
// with it; the root shrinks when it is left with a single child.
func (tx *Tx) Delete(key int64) error {
	if tx.done {
		return ErrTxDone
	}
	path, leaf, err := tx.descend(key)
	if err != nil {
		return err
	}
	i := sort.Search(len(leaf.keys), func(i int) bool { return leaf.keys[i] >= key })
	if i >= len(leaf.keys) || leaf.keys[i] != key {
		return ErrNotFound
	}
	leaf.keys = append(leaf.keys[:i], leaf.keys[i+1:]...)
	leaf.vals = append(leaf.vals[:i], leaf.vals[i+1:]...)
	tx.rebalance(path, leaf)
	root, err := tx.load(tx.root)
	if err != nil {
		return err
	}
	if !root.leaf || len(root.keys) > 0 {
		return tx.repairPath(key)
	}
	return nil
}

// rebalance repairs underflow from cur upwards. path holds the cow-copied
// ancestors of cur, innermost last.
func (tx *Tx) rebalance(path []frame, cur *node) {
	for len(path) > 0 {
		if len(cur.keys) >= tx.tree.minKeys(cur) {
			return
		}
		f := path[len(path)-1]
		parent, idx := f.n, f.idx

		var left, right *node
		if idx > 0 {
			left, _ = tx.cow(parent.children[idx-1])
			parent.children[idx-1] = left.id
		}
		if idx+1 < len(parent.children) {
			right, _ = tx.cow(parent.children[idx+1])
			parent.children[idx+1] = right.id
		}

		min := tx.tree.minKeys(cur)
		switch {
		case left != nil && len(left.keys) > min:
			tx.borrowFromLeft(parent, idx, left, cur)
			return
		case right != nil && len(right.keys) > min:
			tx.borrowFromRight(parent, idx, cur, right)
			return
		case left != nil:
			// Merge cur into left, drop the separator at idx-1.
			tx.mergeNodes(parent, idx-1, left, cur)
			delete(tx.dirty, cur.id)
		default:
			// Merge right into cur, drop the separator at idx.
			tx.mergeNodes(parent, idx, cur, right)
			delete(tx.dirty, right.id)
		}
		cur = parent
		path = path[:len(path)-1]
	}
	// Root handling: shrink when the root internal node has a single child.
	if !cur.leaf && len(cur.keys) == 0 {
		delete(tx.dirty, cur.id)
		tx.root = cur.children[0]
	}
}

// borrowFromLeft moves the left sibling's last entry into cur (leaf), or
// rotates the parent separator through cur (internal).
func (tx *Tx) borrowFromLeft(parent *node, idx int, left, cur *node) {
	if cur.leaf {
		cur.keys = insertInt64(cur.keys, 0, left.keys[len(left.keys)-1])
		cur.vals = insertBytes(cur.vals, 0, left.vals[len(left.vals)-1])
		left.keys = left.keys[:len(left.keys)-1]
		left.vals = left.vals[:len(left.vals)-1]
		parent.keys[idx-1] = cur.keys[0]
		return
	}
	cur.keys = insertInt64(cur.keys, 0, parent.keys[idx-1])
	cur.children = insertPageID(cur.children, 0, left.children[len(left.children)-1])
	parent.keys[idx-1] = left.keys[len(left.keys)-1]
	left.keys = left.keys[:len(left.keys)-1]
	left.children = left.children[:len(left.children)-1]
}

// borrowFromRight moves the right sibling's first entry into cur (leaf), or
// rotates the parent separator through cur (internal).
func (tx *Tx) borrowFromRight(parent *node, idx int, cur, right *node) {
	if cur.leaf {
		cur.keys = append(cur.keys, right.keys[0])
		cur.vals = append(cur.vals, right.vals[0])
		right.keys = right.keys[1:]
		right.vals = right.vals[1:]
		parent.keys[idx] = right.keys[0]
		return
	}
	cur.keys = append(cur.keys, parent.keys[idx])
	cur.children = append(cur.children, right.children[0])
	parent.keys[idx] = right.keys[0]
	right.keys = right.keys[1:]
	right.children = right.children[1:]
}

// mergeNodes merges right into left, removing the separator at parent.keys[sepIdx].
func (tx *Tx) mergeNodes(parent *node, sepIdx int, left, right *node) {
	if left.leaf {
		left.keys = append(left.keys, right.keys...)
		left.vals = append(left.vals, right.vals...)
	} else {
		left.keys = append(left.keys, parent.keys[sepIdx])
		left.keys = append(left.keys, right.keys...)
		left.children = append(left.children, right.children...)
	}
	parent.keys = append(parent.keys[:sepIdx], parent.keys[sepIdx+1:]...)
	parent.children = append(parent.children[:sepIdx+1], parent.children[sepIdx+2:]...)
}

// Get reads a key inside the transaction (read-your-writes).
func (tx *Tx) Get(key int64) ([]byte, error) {
	if tx.done {
		return nil, ErrTxDone
	}
	cur, err := tx.load(tx.root)
	if err != nil {
		return nil, err
	}
	for !cur.leaf {
		i := sort.Search(len(cur.keys), func(i int) bool { return key < cur.keys[i] })
		if cur, err = tx.load(cur.children[i]); err != nil {
			return nil, err
		}
	}
	i := sort.Search(len(cur.keys), func(i int) bool { return cur.keys[i] >= key })
	if i < len(cur.keys) && cur.keys[i] == key {
		return append([]byte(nil), cur.vals[i]...), nil
	}
	return nil, ErrNotFound
}

// Commit writes all dirty pages and atomically publishes the new root. If any
// page write fails, no new root is published and the tree is unchanged.
// Pages written before the failure are unreachable garbage, reclaimed by
// Collect.
func (tx *Tx) Commit() error {
	if tx.done {
		return ErrTxDone
	}
	tx.done = true
	t := tx.tree
	defer t.txMu.Unlock()
	for _, n := range tx.dirty {
		if err := t.store.Write(n.id, n.encode(t.store.PageSize())); err != nil {
			return err
		}
	}
	t.mu.Lock()
	old := t.root
	t.root = tx.root
	t.version++
	t.activeRoots[tx.root]++
	if t.activeRoots[old] <= 1 {
		delete(t.activeRoots, old)
	} else {
		t.activeRoots[old]--
	}
	t.mu.Unlock()
	return nil
}

// Rollback discards the transaction. Pages it allocated become unreachable
// garbage, reclaimed by Collect.
func (tx *Tx) Rollback() {
	if tx.done {
		return
	}
	tx.done = true
	tx.tree.txMu.Unlock()
}

func insertInt64(s []int64, i int, v int64) []int64 {
	s = append(s, 0)
	copy(s[i+1:], s[i:])
	s[i] = v
	return s
}

func insertBytes(s [][]byte, i int, v []byte) [][]byte {
	s = append(s, nil)
	copy(s[i+1:], s[i:])
	s[i] = v
	return s
}

func insertPageID(s []PageID, i int, v PageID) []PageID {
	s = append(s, 0)
	copy(s[i+1:], s[i:])
	s[i] = v
	return s
}

func copyBytes(s [][]byte) [][]byte {
	out := make([][]byte, len(s))
	for i, v := range s {
		out[i] = append([]byte(nil), v...)
	}
	return out
}
