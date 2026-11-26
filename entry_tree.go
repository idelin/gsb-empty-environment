package gin

import (
	"fmt"
	"sort"
)

// EntryTree is an ordered AVL tree mapping index keys to postings. Ordered
// traversal makes flush and diagnostic output deterministic.
type EntryTree struct {
	root *entryNode
	size int
}

type entryNode struct {
	key     string
	posting *Posting
	left    *entryNode
	right   *entryNode
	height  int
}

// NewEntryTree returns an empty entry tree.
func NewEntryTree() *EntryTree {
	return &EntryTree{}
}

// Len returns the number of indexed keys.
func (t *EntryTree) Len() int {
	if t == nil {
		return 0
	}
	return t.size
}

// Height returns the tree height. An empty tree has height zero.
func (t *EntryTree) Height() int {
	if t == nil {
		return 0
	}
	return entryHeight(t.root)
}

// Get returns the posting for key, or nil when key is absent.
func (t *EntryTree) Get(key string) *Posting {
	if t == nil {
		return nil
	}
	node := t.root
	for node != nil {
		switch {
		case key < node.key:
			node = node.left
		case key > node.key:
			node = node.right
		default:
			return node.posting
		}
	}
	return nil
}

// Set inserts or replaces the posting for key.
func (t *EntryTree) Set(key string, posting *Posting) {
	if t == nil {
		return
	}
	var inserted bool
	t.root, inserted = setEntryNode(t.root, key, posting)
	if inserted {
		t.size++
	}
}

func setEntryNode(node *entryNode, key string, posting *Posting) (*entryNode, bool) {
	if node == nil {
		return &entryNode{key: key, posting: posting, height: 1}, true
	}
	var inserted bool
	switch {
	case key < node.key:
		node.left, inserted = setEntryNode(node.left, key, posting)
	case key > node.key:
		node.right, inserted = setEntryNode(node.right, key, posting)
	default:
		node.posting = posting
		return node, false
	}
	node.height = 1 + maxInt(entryHeight(node.left), entryHeight(node.right))
	return rebalanceEntry(node), inserted
}

// Delete removes key from the tree.
func (t *EntryTree) Delete(key string) {
	if t == nil {
		return
	}
	var deleted bool
	t.root, deleted = deleteEntryNode(t.root, key)
	if deleted {
		t.size--
	}
}

func deleteEntryNode(node *entryNode, key string) (*entryNode, bool) {
	if node == nil {
		return nil, false
	}
	var deleted bool
	switch {
	case key < node.key:
		node.left, deleted = deleteEntryNode(node.left, key)
	case key > node.key:
		node.right, deleted = deleteEntryNode(node.right, key)
	default:
		deleted = true
		switch {
		case node.left == nil:
			return node.right, true
		case node.right == nil:
			return node.left, true
		default:
			successor := leftmostEntryNode(node.right)
			node.key = successor.key
			node.posting = successor.posting
			node.right, _ = deleteEntryNode(node.right, successor.key)
		}
	}
	if node == nil {
		return nil, deleted
	}
	node.height = 1 + maxInt(entryHeight(node.left), entryHeight(node.right))
	return rebalanceEntry(node), deleted
}

// Keys returns all keys in ascending order.
func (t *EntryTree) Keys() []string {
	if t == nil {
		return nil
	}
	keys := make([]string, 0, t.size)
	t.Ascend(func(key string, _ *Posting) bool {
		keys = append(keys, key)
		return true
	})
	return keys
}

// Ascend visits entries in key order until fn returns false.
func (t *EntryTree) Ascend(fn func(string, *Posting) bool) {
	if t == nil {
		return
	}
	continuing := true
	var walk func(*entryNode)
	walk = func(node *entryNode) {
		if node == nil || !continuing {
			return
		}
		walk(node.left)
		if !continuing {
			return
		}
		if !fn(node.key, node.posting) {
			continuing = false
			return
		}
		walk(node.right)
	}
	walk(t.root)
}

// Validate checks ordering, balance and posting invariants. It is intended for
// tests and diagnostics.
func (t *EntryTree) Validate() error {
	if t == nil {
		return nil
	}
	keys := t.Keys()
	if !sort.StringsAreSorted(keys) {
		return fmt.Errorf("entry tree keys are not sorted")
	}
	for i := 1; i < len(keys); i++ {
		if keys[i] == keys[i-1] {
			return fmt.Errorf("entry tree contains duplicate key %q", keys[i])
		}
	}
	if err := validateEntryNode(t.root); err != nil {
		return err
	}
	if len(keys) != t.size {
		return fmt.Errorf("entry tree size mismatch: metadata=%d actual=%d", t.size, len(keys))
	}
	return nil
}

func validateEntryNode(node *entryNode) error {
	if node == nil {
		return nil
	}
	if node.posting == nil {
		return fmt.Errorf("entry %q has nil posting", node.key)
	}
	if balance := entryHeight(node.left) - entryHeight(node.right); balance < -1 || balance > 1 {
		return fmt.Errorf("entry %q is unbalanced: %d", node.key, balance)
	}
	if node.left != nil && node.left.key >= node.key {
		return fmt.Errorf("entry %q violates left ordering", node.key)
	}
	if node.right != nil && node.right.key <= node.key {
		return fmt.Errorf("entry %q violates right ordering", node.key)
	}
	return nil
}

func rebalanceEntry(node *entryNode) *entryNode {
	balance := entryHeight(node.left) - entryHeight(node.right)
	if balance > 1 {
		if entryHeight(node.left.left) < entryHeight(node.left.right) {
			node.left = rotateEntryLeft(node.left)
		}
		return rotateEntryRight(node)
	}
	if balance < -1 {
		if entryHeight(node.right.right) < entryHeight(node.right.left) {
			node.right = rotateEntryRight(node.right)
		}
		return rotateEntryLeft(node)
	}
	return node
}

func rotateEntryLeft(node *entryNode) *entryNode {
	right := node.right
	node.right = right.left
	right.left = node
	node.height = 1 + maxInt(entryHeight(node.left), entryHeight(node.right))
	right.height = 1 + maxInt(entryHeight(right.left), entryHeight(right.right))
	return right
}

func rotateEntryRight(node *entryNode) *entryNode {
	left := node.left
	node.left = left.right
	left.right = node
	node.height = 1 + maxInt(entryHeight(node.left), entryHeight(node.right))
	left.height = 1 + maxInt(entryHeight(left.left), entryHeight(left.right))
	return left
}

func leftmostEntryNode(node *entryNode) *entryNode {
	for node != nil && node.left != nil {
		node = node.left
	}
	return node
}

func entryHeight(node *entryNode) int {
	if node == nil {
		return 0
	}
	return node.height
}
