package bptree

import "sort"

const (
	maxLeafKeys     = 32
	maxInternalKeys = 32
)

// node is the serialized content of a page. A node is either an
// internal routing node or a leaf; leaves are chained through next.
type node struct {
	isLeaf   bool
	keys     []int
	vals     []int    // leaf only, parallel to keys
	children []PageID // internal only, len(keys)+1
	next     PageID   // leaf only, right sibling or InvalidPageID
}

// BPlusTree is a B+ tree whose nodes live in a BufferPool.
type BPlusTree struct {
	pool *BufferPool
	root PageID
}

// NewBPlusTree creates an empty tree backed by pool.
func NewBPlusTree(pool *BufferPool) (*BPlusTree, error) {
	p, err := pool.NewPage(&node{isLeaf: true, next: InvalidPageID})
	if err != nil {
		return nil, err
	}
	t := &BPlusTree{pool: pool, root: p.ID()}
	if err := pool.UnpinPage(p.ID(), true); err != nil {
		return nil, err
	}
	return t, nil
}

func lowerBound(keys []int, key int) int {
	return sort.Search(len(keys), func(i int) bool { return keys[i] >= key })
}

// childIndex returns the index of the child subtree that holds key.
func childIndex(n *node, key int) int {
	return sort.Search(len(n.keys), func(i int) bool { return key < n.keys[i] })
}

// findLeaf returns the pinned leaf page that may contain key.
// Internal pages are unpinned as soon as their child is pinned,
// so at most one page per tree level is ever pinned.
func (t *BPlusTree) findLeaf(key int) (*Page, error) {
	p, err := t.pool.FetchPage(t.root)
	if err != nil {
		return nil, err
	}
	for !p.node.isLeaf {
		child := p.node.children[childIndex(p.node, key)]
		cp, err := t.pool.FetchPage(child)
		if err != nil {
			_ = t.pool.UnpinPage(p.id, false)
			return nil, err
		}
		if err := t.pool.UnpinPage(p.id, false); err != nil {
			return nil, err
		}
		p = cp
	}
	return p, nil
}

// Insert adds or replaces the (key, val) record.
func (t *BPlusTree) Insert(key, val int) error {
	promoted, newChild, split, err := t.insert(t.root, key, val)
	if err != nil {
		return err
	}
	if !split {
		return nil
	}
	p, err := t.pool.NewPage(&node{
		isLeaf:   false,
		keys:     []int{promoted},
		children: []PageID{t.root, newChild},
	})
	if err != nil {
		return err
	}
	t.root = p.ID()
	return t.pool.UnpinPage(p.ID(), true)
}

// insert returns (promotedKey, newRightPage, didSplit, err).
func (t *BPlusTree) insert(id PageID, key, val int) (int, PageID, bool, error) {
	p, err := t.pool.FetchPage(id)
	if err != nil {
		return 0, InvalidPageID, false, err
	}
	n := p.node

	if n.isLeaf {
		pos := lowerBound(n.keys, key)
		if pos < len(n.keys) && n.keys[pos] == key {
			n.vals[pos] = val
			return 0, InvalidPageID, false, t.pool.UnpinPage(id, true)
		}
		n.keys = insertInt(n.keys, pos, key)
		n.vals = insertInt(n.vals, pos, val)
		if len(n.keys) <= maxLeafKeys {
			return 0, InvalidPageID, false, t.pool.UnpinPage(id, true)
		}
		// Split the leaf; the separator is copied up.
		mid := len(n.keys) / 2
		right := &node{
			isLeaf: true,
			keys:   append([]int(nil), n.keys[mid:]...),
			vals:   append([]int(nil), n.vals[mid:]...),
			next:   n.next,
		}
		rp, err := t.pool.NewPage(right)
		if err != nil {
			_ = t.pool.UnpinPage(id, true)
			return 0, InvalidPageID, false, err
		}
		n.keys = n.keys[:mid]
		n.vals = n.vals[:mid]
		n.next = rp.ID()
		if err := t.pool.UnpinPage(rp.ID(), true); err != nil {
			return 0, InvalidPageID, false, err
		}
		if err := t.pool.UnpinPage(id, true); err != nil {
			return 0, InvalidPageID, false, err
		}
		return right.keys[0], rp.ID(), true, nil
	}

	idx := childIndex(n, key)
	promoted, newChild, split, err := t.insert(n.children[idx], key, val)
	if err != nil {
		_ = t.pool.UnpinPage(id, false)
		return 0, InvalidPageID, false, err
	}
	if !split {
		return 0, InvalidPageID, false, t.pool.UnpinPage(id, false)
	}
	n.keys = insertInt(n.keys, idx, promoted)
	n.children = insertPageID(n.children, idx+1, newChild)
	if len(n.keys) <= maxInternalKeys {
		return 0, InvalidPageID, false, t.pool.UnpinPage(id, true)
	}
	// Split the internal node; the middle key moves up.
	mid := len(n.keys) / 2
	up := n.keys[mid]
	right := &node{
		isLeaf:   false,
		keys:     append([]int(nil), n.keys[mid+1:]...),
		children: append([]PageID(nil), n.children[mid+1:]...),
	}
	rp, err := t.pool.NewPage(right)
	if err != nil {
		_ = t.pool.UnpinPage(id, true)
		return 0, InvalidPageID, false, err
	}
	n.keys = n.keys[:mid]
	n.children = n.children[:mid+1]
	if err := t.pool.UnpinPage(rp.ID(), true); err != nil {
		return 0, InvalidPageID, false, err
	}
	if err := t.pool.UnpinPage(id, true); err != nil {
		return 0, InvalidPageID, false, err
	}
	return up, rp.ID(), true, nil
}

func insertInt(s []int, i, v int) []int {
	s = append(s, 0)
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
