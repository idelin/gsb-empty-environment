package btree

import "bytes"

// txn is a single copy-on-write write transaction (one Insert).
//
// Nodes are addressed by stable logical IDs. Modified nodes keep their ID
// and are rewritten at a new file offset on commit; brand-new nodes (split
// right siblings, new roots) get fresh IDs appended to the node map. Because
// parent->child pointers and the leaf chain store logical IDs, a COW rewrite
// of a leaf never invalidates its predecessor's next pointer.
type txn struct {
	d      *DB
	m      []uint64         // working copy of the node map
	pages  map[uint32]*node // dirty nodes to write, keyed by node ID
	rootID uint32
}

func (d *DB) begin() *txn {
	return &txn{
		d:      d,
		m:      append([]uint64(nil), d.m...),
		pages:  make(map[uint32]*node),
		rootID: d.rootID,
	}
}

// load returns a private, mutable copy of node id for this transaction.
func (tx *txn) load(id uint32) (*node, error) {
	if n, ok := tx.pages[id]; ok {
		return n, nil
	}
	n, err := tx.d.readNode(id, tx.m)
	if err != nil {
		return nil, err
	}
	tx.pages[id] = n
	return n, nil
}

// alloc assigns a fresh logical ID to a new node.
func (tx *txn) alloc(n *node) uint32 {
	id := uint32(len(tx.m))
	n.id = id
	tx.m = append(tx.m, 0) // offset assigned at commit
	tx.pages[id] = n
	return id
}

// Insert adds or overwrites one key and commits it atomically.
func (d *DB) Insert(key, value []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.crashed {
		return ErrCrashed
	}
	if len(key) > d.MaxKeySize() {
		return ErrKeyTooLarge
	}
	if len(value) > d.MaxValueSize(len(key)) {
		return ErrValueTooLarge
	}
	key = append([]byte(nil), key...)
	value = append([]byte(nil), value...)

	tx := d.begin()
	root, err := tx.load(tx.rootID)
	if err != nil {
		return err
	}
	_, _, splitKey, splitID, split, err := tx.insert(root, key, value)
	if err != nil {
		return err
	}
	if split {
		// Root split: a brand-new root ID is published by the superblock.
		// The old root ID keeps its old offset in the old map, so snapshots
		// taken before this commit still see the complete old tree.
		nr := &node{
			typ:      nodeInternal,
			keys:     [][]byte{splitKey},
			children: []uint32{root.id, splitID},
		}
		tx.alloc(nr)
		tx.rootID = nr.id
	}
	return d.commit(tx)
}

// insert recursively inserts into n (already a private txn copy).
//
// It reports whether n's minimum key decreased (so the parent can refresh
// its separator), and, on overflow, the split: splitKey is the first key of
// the new right sibling splitID.
func (tx *txn) insert(n *node, key, val []byte) (minChanged bool, newMin, splitKey []byte, splitID uint32, split bool, err error) {
	if n.isLeaf() {
		i := lowerBound(n.keys, key)
		if i < len(n.keys) && bytes.Equal(n.keys[i], key) {
			n.vals[i] = val // duplicate key: overwrite
		} else {
			n.keys = insertBytes(n.keys, i, key)
			n.vals = insertBytes(n.vals, i, val)
			if i == 0 && len(n.keys) > 1 {
				minChanged = true
				newMin = n.keys[0]
			}
		}
		if n.size() <= tx.d.pageSize {
			return minChanged, newMin, nil, 0, false, nil
		}
		sp := leafSplitPoint(n)
		right := &node{typ: nodeLeaf, next: n.next}
		right.keys = append([][]byte(nil), n.keys[sp:]...)
		right.vals = append([][]byte(nil), n.vals[sp:]...)
		n.keys = n.keys[:sp]
		n.vals = n.vals[:sp]
		rid := tx.alloc(right)
		n.next = rid
		return minChanged, newMin, right.keys[0], rid, true, nil
	}

	i := n.childIndex(key)
	child, err := tx.load(n.children[i])
	if err != nil {
		return false, nil, nil, 0, false, err
	}
	cMinChanged, cMin, cSplitKey, cSplitID, cSplit, err := tx.insert(child, key, val)
	if err != nil {
		return false, nil, nil, 0, false, err
	}
	if cMinChanged {
		if i > 0 {
			n.keys[i-1] = cMin // refresh separator: keys[i-1] == min(child_i)
		} else {
			minChanged = true
			newMin = cMin
		}
	}
	if cSplit {
		n.keys = insertBytes(n.keys, i, cSplitKey)
		n.children = insertUint32(n.children, i+1, cSplitID)
	}
	if n.size() <= tx.d.pageSize {
		return minChanged, newMin, nil, 0, false, nil
	}
	// Split internal node: promote the middle key.
	mid := len(n.keys) / 2
	if mid < 1 {
		mid = 1
	}
	if mid > len(n.keys)-2 {
		mid = len(n.keys) - 2
	}
	promote := n.keys[mid]
	right := &node{typ: nodeInternal}
	right.keys = append([][]byte(nil), n.keys[mid+1:]...)
	right.children = append([]uint32(nil), n.children[mid+1:]...)
	n.keys = n.keys[:mid]
	n.children = n.children[:mid+1]
	rid := tx.alloc(right)
	return minChanged, newMin, promote, rid, true, nil
}

// leafSplitPoint picks the cell index where the right sibling starts, aiming
// for half the encoded size and keeping both sides non-empty.
func leafSplitPoint(n *node) int {
	half := n.size() / 2
	acc := nodeHdrSize + nodeCRCSize
	sp := len(n.keys) - 1
	for j, k := range n.keys {
		acc += 6 + len(k) + len(n.vals[j])
		if acc >= half {
			sp = j + 1
			break
		}
	}
	if sp < 1 {
		sp = 1
	}
	if sp > len(n.keys)-1 {
		sp = len(n.keys) - 1
	}
	return sp
}
