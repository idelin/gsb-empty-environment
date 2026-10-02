package bptree

import (
	"slices"
	"sort"
)

const (
	// maxLeafKeys is the max number of key/value pairs in a leaf page.
	maxLeafKeys = 64
	// maxInternalKeys is the max number of separator keys in an internal page.
	maxInternalKeys = 64
)

// node is the in-page B+ tree node. Leaf nodes store values and a next
// pointer forming the leaf-level linked list; internal nodes store
// separator keys and child page IDs.
type node struct {
	isLeaf   bool
	keys     []int64
	vals     []int64  // leaf only, parallel to keys
	children []PageID // internal only, len == len(keys)+1
	next     PageID   // leaf only, right sibling or InvalidPageID
}

// BPlusTree is a disk-oriented B+ tree whose pages are managed by a
// BufferPool. Every page access goes through Fetch/Unpin.
type BPlusTree struct {
	pool *BufferPool
	root PageID
}

func NewBPlusTree(pool *BufferPool) (*BPlusTree, error) {
	pg, err := pool.NewPage()
	if err != nil {
		return nil, err
	}
	pg.node = &node{isLeaf: true, next: InvalidPageID}
	t := &BPlusTree{pool: pool, root: pg.ID}
	pool.Unpin(pg.ID)
	return t, nil
}

// findLeaf descends from the root to the leaf that may contain key,
// returning the leaf page pinned. Each parent is unpinned as soon as its
// child is pinned, so at most two pages are held at any time.
func (t *BPlusTree) findLeaf(key int64) (*Page, error) {
	pg, err := t.pool.Fetch(t.root)
	if err != nil {
		return nil, err
	}
	for !pg.node.isLeaf {
		i := sort.Search(len(pg.node.keys), func(i int) bool { return key < pg.node.keys[i] })
		child, err := t.pool.Fetch(pg.node.children[i])
		t.pool.Unpin(pg.ID)
		if err != nil {
			return nil, err
		}
		pg = child
	}
	return pg, nil
}

// Insert adds or updates key with value val, splitting pages as needed.
func (t *BPlusTree) Insert(key, val int64) error {
	promote, newPID, split, err := t.insert(t.root, key, val)
	if err != nil {
		return err
	}
	if !split {
		return nil
	}
	root, err := t.pool.NewPage()
	if err != nil {
		return err
	}
	root.node = &node{
		isLeaf:   false,
		keys:     []int64{promote},
		children: []PageID{t.root, newPID},
	}
	t.root = root.ID
	t.pool.Unpin(root.ID)
	return nil
}

// insert returns (promoteKey, newPageID, didSplit, err).
func (t *BPlusTree) insert(pid PageID, key, val int64) (int64, PageID, bool, error) {
	pg, err := t.pool.Fetch(pid)
	if err != nil {
		return 0, 0, false, err
	}
	defer t.pool.Unpin(pid)
	n := pg.node

	if n.isLeaf {
		i := sort.Search(len(n.keys), func(i int) bool { return n.keys[i] >= key })
		if i < len(n.keys) && n.keys[i] == key {
			n.vals[i] = val
			return 0, 0, false, nil
		}
		n.keys = slices.Insert(n.keys, i, key)
		n.vals = slices.Insert(n.vals, i, val)
		if len(n.keys) <= maxLeafKeys {
			return 0, 0, false, nil
		}
		return t.splitLeaf(pg)
	}

	i := sort.Search(len(n.keys), func(i int) bool { return key < n.keys[i] })
	promote, newPID, split, err := t.insert(n.children[i], key, val)
	if err != nil || !split {
		return 0, 0, false, err
	}
	n.keys = slices.Insert(n.keys, i, promote)
	n.children = slices.Insert(n.children, i+1, newPID)
	if len(n.keys) <= maxInternalKeys {
		return 0, 0, false, nil
	}
	return t.splitInternal(pg)
}

// splitLeaf splits a full leaf in half and links the new right page into
// the leaf chain. The first key of the right page is promoted.
func (t *BPlusTree) splitLeaf(pg *Page) (int64, PageID, bool, error) {
	n := pg.node
	mid := len(n.keys) / 2
	right, err := t.pool.NewPage()
	if err != nil {
		return 0, 0, false, err
	}
	rn := &node{isLeaf: true, next: n.next}
	rn.keys = append(rn.keys, n.keys[mid:]...)
	rn.vals = append(rn.vals, n.vals[mid:]...)
	n.keys = n.keys[:mid]
	n.vals = n.vals[:mid]
	n.next = right.ID
	right.node = rn
	promote := rn.keys[0]
	t.pool.Unpin(right.ID)
	return promote, right.ID, true, nil
}

// splitInternal splits a full internal page, pushing the middle key up.
func (t *BPlusTree) splitInternal(pg *Page) (int64, PageID, bool, error) {
	n := pg.node
	mid := len(n.keys) / 2
	right, err := t.pool.NewPage()
	if err != nil {
		return 0, 0, false, err
	}
	rn := &node{isLeaf: false}
	rn.keys = append(rn.keys, n.keys[mid+1:]...)
	rn.children = append(rn.children, n.children[mid+1:]...)
	promote := n.keys[mid]
	n.keys = n.keys[:mid]
	n.children = n.children[:mid+1]
	right.node = rn
	t.pool.Unpin(right.ID)
	return promote, right.ID, true, nil
}
