package bptree

import (
	"fmt"
	"slices"
	"sort"
)

// Tx is a copy-on-write write transaction. All modifications are applied to
// freshly allocated pages; nothing is published until commit.
type Tx struct {
	t *txState
}

type allocRec struct {
	id       uint64
	fromFree bool
}

type txState struct {
	db     *DB
	seq    uint64
	root   uint64
	dirty  map[uint64]*node  // new page id -> node
	copied map[uint64]uint64 // old page id -> new page id
	allocs []allocRec
}

// allocPage allocates a page id for this transaction.
func (t *txState) allocPage() uint64 {
	id, fromFree := t.db.p.alloc()
	t.allocs = append(t.allocs, allocRec{id, fromFree})
	return id
}

// rollback releases every page allocated by this transaction. Used when the
// transaction fails: no new root is ever published.
func (t *txState) rollback() {
	for i := len(t.allocs) - 1; i >= 0; i-- {
		t.db.p.rollbackAlloc(t.allocs[i].id, t.allocs[i].fromFree)
	}
}

// load returns the node for id: the dirty copy if this tx created one,
// otherwise the immutable on-disk version.
func (t *txState) load(id uint64) (*node, error) {
	if n, ok := t.dirty[id]; ok {
		return n, nil
	}
	return t.db.p.readNode(id)
}

// cow returns a writable copy of the node, allocating a new page id on first
// use. The original page is never modified.
func (t *txState) cow(id uint64) (*node, error) {
	if n, ok := t.dirty[id]; ok {
		return n, nil
	}
	if nid, ok := t.copied[id]; ok {
		return t.dirty[nid], nil
	}
	n, err := t.load(id)
	if err != nil {
		return nil, err
	}
	c := n.clone()
	c.id = t.allocPage()
	c.version = t.seq
	t.dirty[c.id] = c
	t.copied[id] = c.id
	return c, nil
}

// Put inserts or overwrites a key. Duplicate keys are overwritten.
func (tx *Tx) Put(key int64, value []byte) error {
	if len(value) > MaxValueSize {
		return ErrValueTooLarge
	}
	t := tx.t
	newRoot, splitKey, rightID, didSplit, err := t.insert(t.root, key, value)
	if err != nil {
		return err
	}
	if didSplit {
		r := &node{
			id:      t.allocPage(),
			version: t.seq,
			leaf:    false,
			keys:    []int64{splitKey},
			child:   []uint64{newRoot, rightID},
		}
		t.dirty[r.id] = r
		t.root = r.id
	} else {
		t.root = newRoot
	}
	return nil
}

// Get reads a key inside the transaction (read-your-writes).
func (tx *Tx) Get(key int64) ([]byte, error) {
	return tx.t.db.getFrom(tx.t.root, key)
}

// Delete removes a key. Deleting a missing key is a no-op.
func (tx *Tx) Delete(key int64) error {
	t := tx.t
	newRoot, _, _, err := t.delete(t.root, key)
	if err != nil {
		return err
	}
	t.root = newRoot
	// Root shrink: an internal root with 0 keys is replaced by its only child.
	root, err := t.load(t.root)
	if err != nil {
		return err
	}
	if !root.leaf && len(root.keys) == 0 {
		t.root = root.child[0]
	}
	return nil
}

// insert returns (newID, splitKey, rightID, didSplit, err).
func (t *txState) insert(id uint64, key int64, val []byte) (uint64, int64, uint64, bool, error) {
	n, err := t.cow(id)
	if err != nil {
		return 0, 0, 0, false, err
	}
	if n.leaf {
		i := sort.Search(len(n.keys), func(i int) bool { return n.keys[i] >= key })
		if i < len(n.keys) && n.keys[i] == key {
			n.values[i] = append([]byte(nil), val...)
			return n.id, 0, 0, false, nil
		}
		n.keys = slices.Insert(n.keys, i, key)
		n.values = slices.Insert(n.values, i, append([]byte(nil), val...))
		if len(n.keys) <= t.db.capacity {
			return n.id, 0, 0, false, nil
		}
		// Split leaf: right half goes to a new page; preserve the link chain.
		half := len(n.keys) / 2
		r := &node{
			id:      t.allocPage(),
			version: t.seq,
			leaf:    true,
			keys:    append([]int64(nil), n.keys[half:]...),
			values:  cloneVals(n.values[half:]),
			next:    n.next,
		}
		t.dirty[r.id] = r
		n.keys = n.keys[:half]
		n.values = n.values[:half]
		n.next = r.id
		return n.id, r.keys[0], r.id, true, nil
	}
	i := sort.Search(len(n.keys), func(i int) bool { return key < n.keys[i] })
	newChild, splitKey, rightID, didSplit, err := t.insert(n.child[i], key, val)
	if err != nil {
		return 0, 0, 0, false, err
	}
	n.child[i] = newChild
	if didSplit {
		n.keys = slices.Insert(n.keys, i, splitKey)
		n.child = slices.Insert(n.child, i+1, rightID)
	}
	if len(n.keys) <= t.db.capacity {
		return n.id, 0, 0, false, nil
	}
	// Split internal: push the middle separator up.
	half := len(n.keys) / 2
	up := n.keys[half]
	r := &node{
		id:      t.allocPage(),
		version: t.seq,
		leaf:    false,
		keys:    append([]int64(nil), n.keys[half+1:]...),
		child:   append([]uint64(nil), n.child[half+1:]...),
	}
	t.dirty[r.id] = r
	n.keys = n.keys[:half]
	n.child = n.child[:half+1]
	return n.id, up, r.id, true, nil
}

// delete returns (newID, changed, underflow, err). changed reports whether
// the subtree was modified; note the id alone cannot signal this, because a
// node already copied by this transaction keeps its id when modified again.
func (t *txState) delete(id uint64, key int64) (uint64, bool, bool, error) {
	n0, err := t.load(id)
	if err != nil {
		return 0, false, false, err
	}
	if n0.leaf {
		i := sort.Search(len(n0.keys), func(i int) bool { return n0.keys[i] >= key })
		if i >= len(n0.keys) || n0.keys[i] != key {
			return id, false, false, nil // no-op
		}
		n, err := t.cow(id)
		if err != nil {
			return 0, false, false, err
		}
		n.keys = slices.Delete(n.keys, i, i+1)
		n.values = slices.Delete(n.values, i, i+1)
		return n.id, true, len(n.keys) < t.db.minKeys, nil
	}
	i := sort.Search(len(n0.keys), func(i int) bool { return key < n0.keys[i] })
	newChild, changed, under, err := t.delete(n0.child[i], key)
	if err != nil {
		return 0, false, false, err
	}
	if !changed {
		return id, false, false, nil
	}
	n, err := t.cow(id)
	if err != nil {
		return 0, false, false, err
	}
	n.child[i] = newChild
	if i > 0 {
		// The child's minimum key may have shrunk; keep the separator
		// equal to the minimum key of the right subtree.
		mk, err := t.leftmost(newChild)
		if err != nil {
			return 0, false, false, err
		}
		n.keys[i-1] = mk
	}
	if under {
		if err := t.rebalance(n, i); err != nil {
			return 0, false, false, err
		}
	}
	return n.id, true, len(n.keys) < t.db.minKeys, nil
}

// rebalance fixes an underflowed child (n.child[i], already a dirty copy) by
// borrowing from a sibling or merging with it. Leaf link pointers are fixed
// up locally so that pages created by this commit form a correct chain.
func (t *txState) rebalance(n *node, i int) error {
	child := t.dirty[n.child[i]]
	min := t.db.minKeys
	// Borrow from the left sibling.
	if i > 0 {
		left, err := t.load(n.child[i-1])
		if err != nil {
			return err
		}
		if len(left.keys) > min {
			lc, err := t.cow(left.id)
			if err != nil {
				return err
			}
			n.child[i-1] = lc.id
			if child.leaf {
				child.keys = slices.Insert(child.keys, 0, lc.keys[len(lc.keys)-1])
				child.values = slices.Insert(child.values, 0, lc.values[len(lc.values)-1])
				lc.keys = lc.keys[:len(lc.keys)-1]
				lc.values = lc.values[:len(lc.values)-1]
				n.keys[i-1] = child.keys[0]
				lc.next = child.id
			} else {
				child.keys = slices.Insert(child.keys, 0, n.keys[i-1])
				child.child = slices.Insert(child.child, 0, lc.child[len(lc.child)-1])
				n.keys[i-1] = lc.keys[len(lc.keys)-1]
				lc.keys = lc.keys[:len(lc.keys)-1]
				lc.child = lc.child[:len(lc.child)-1]
			}
			return nil
		}
	}
	// Borrow from the right sibling.
	if i < len(n.child)-1 {
		right, err := t.load(n.child[i+1])
		if err != nil {
			return err
		}
		if len(right.keys) > min {
			rc, err := t.cow(right.id)
			if err != nil {
				return err
			}
			n.child[i+1] = rc.id
			if child.leaf {
				child.keys = append(child.keys, rc.keys[0])
				child.values = append(child.values, rc.values[0])
				rc.keys = rc.keys[1:]
				rc.values = rc.values[1:]
				n.keys[i] = rc.keys[0]
				child.next = rc.id
			} else {
				child.keys = append(child.keys, n.keys[i])
				child.child = append(child.child, rc.child[0])
				n.keys[i] = rc.keys[0]
				rc.keys = rc.keys[1:]
				rc.child = rc.child[1:]
			}
			return nil
		}
	}
	// Merge. Prefer merging into the left sibling.
	if i > 0 {
		lc, err := t.cow(n.child[i-1])
		if err != nil {
			return err
		}
		n.child[i-1] = lc.id
		if child.leaf {
			lc.keys = append(lc.keys, child.keys...)
			lc.values = append(lc.values, child.values...)
			lc.next = child.next
		} else {
			lc.keys = append(lc.keys, n.keys[i-1])
			lc.keys = append(lc.keys, child.keys...)
			lc.child = append(lc.child, child.child...)
		}
		n.keys = slices.Delete(n.keys, i-1, i)
		n.child = slices.Delete(n.child, i, i+1)
		return nil
	}
	// Merge the right sibling into the child.
	rc, err := t.cow(n.child[1])
	if err != nil {
		return err
	}
	if child.leaf {
		child.keys = append(child.keys, rc.keys...)
		child.values = append(child.values, rc.values...)
		child.next = rc.next
	} else {
		child.keys = append(child.keys, n.keys[0])
		child.keys = append(child.keys, rc.keys...)
		child.child = append(child.child, rc.child...)
	}
	n.keys = slices.Delete(n.keys, 0, 1)
	n.child = slices.Delete(n.child, 1, 2)
	return nil
}

// leftmost returns the smallest key in the subtree rooted at id.
func (t *txState) leftmost(id uint64) (int64, error) {
	for {
		n, err := t.load(id)
		if err != nil {
			return 0, err
		}
		if n.leaf {
			if len(n.keys) == 0 {
				return 0, fmt.Errorf("bptree: empty subtree at page %d", id)
			}
			return n.keys[0], nil
		}
		id = n.child[0]
	}
}

func cloneVals(v [][]byte) [][]byte {
	out := make([][]byte, len(v))
	for i, b := range v {
		out[i] = append([]byte(nil), b...)
	}
	return out
}

// fixLeafLinks runs at commit time and rewrites the next pointer of every
// leaf this transaction created so it matches the in-order leaf layout of
// the new tree. This is required because a transaction may copy many
// scattered leaves: an early copy's link would otherwise keep pointing at
// the old page id of a successor that was itself copied later in the same
// transaction. Clean (shared, older-version) leaves are immutable and keep
// their links; iterators never trust those across versions.
func (t *txState) fixLeafLinks() error {
	var order []uint64
	var walk func(id uint64) error
	walk = func(id uint64) error {
		n, err := t.load(id)
		if err != nil {
			return err
		}
		if n.leaf {
			order = append(order, id)
			return nil
		}
		for _, c := range n.child {
			if err := walk(c); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(t.root); err != nil {
		return err
	}
	for i, id := range order {
		n, ok := t.dirty[id]
		if !ok {
			continue
		}
		var next uint64
		if i+1 < len(order) {
			next = order[i+1]
		}
		n.next = next
	}
	return nil
}
