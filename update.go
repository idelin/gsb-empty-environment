package pathtree

import (
	"bytes"
	"sort"
)

// Update describes one key/value change for Batch. A zero Value inserts an
// empty byte-slice value; set Delete true to remove a key.
type Update struct {
	Key    string
	Value  []byte
	Delete bool
}

type kvUpdate struct {
	key    string
	value  []byte
	delete bool
}

// Put returns a new tree containing key mapped to a private copy of value.
func (t *Tree) Put(key string, value []byte) *Tree {
	root, _ := putNode(t.root, key, cloneBytes(value), nil)
	return &Tree{root: root}
}

// Delete returns a new tree without key. If key is absent, it returns the
// same tree pointer.
func (t *Tree) Delete(key string) *Tree {
	root, _, _ := deleteNode(t.root, key, nil)
	return &Tree{root: root}
}

// Batch applies a sorted batch in one recursive traversal. Several updates in
// the same subtree share the single copied path node above them. The input
// slice is not retained, and equal keys use the last update in input order.
func (t *Tree) Batch(updates []Update) (*Tree, BatchStats) {
	if len(updates) == 0 {
		return t, BatchStats{}
	}

	st := &batchStats{}
	prepared := make([]kvUpdate, len(updates))
	for i, update := range updates {
		prepared[i] = kvUpdate{
			key:    update.Key,
			value:  cloneBytes(update.Value),
			delete: update.Delete,
		}
	}
	sort.SliceStable(prepared, func(i, j int) bool {
		return prepared[i].key < prepared[j].key
	})

	unique := prepared[:0]
	for i := 0; i < len(prepared); {
		j := i + 1
		for j < len(prepared) && prepared[j].key == prepared[i].key {
			j++
		}
		st.superseded += j - i - 1
		unique = append(unique, prepared[j-1])
		i = j
	}

	root := applyBatch(t.root, unique, st)
	return &Tree{root: root}, st.public()
}

func putNode(n *node, key string, value []byte, st *batchStats) (*node, bool) {
	if n == nil {
		return newNode(key, value, nil, nil, st), true
	}

	if key == n.key {
		if bytes.Equal(n.value, value) {
			return n, false
		}
		c := copyNode(n, st)
		c.value = value
		return c, true
	}

	if key < n.key {
		left, changed := putNode(n.left, key, value, st)
		if !changed {
			return n, false
		}
		return rebalanceNode(n, left, n.right, st), true
	}

	right, changed := putNode(n.right, key, value, st)
	if !changed {
		return n, false
	}
	return rebalanceNode(n, n.left, right, st), true
}

func deleteNode(n *node, key string, st *batchStats) (*node, bool, bool) {
	if n == nil {
		return nil, false, false
	}

	if key < n.key {
		left, changed, removed := deleteNode(n.left, key, st)
		if !changed {
			return n, false, removed
		}
		return rebalanceNode(n, left, n.right, st), true, true
	}
	if key > n.key {
		right, changed, removed := deleteNode(n.right, key, st)
		if !changed {
			return n, false, removed
		}
		return rebalanceNode(n, n.left, right, st), true, true
	}

	return mergeTrees(n.left, n.right, st), true, true
}

func applyBatch(n *node, updates []kvUpdate, st *batchStats) *node {
	if n == nil {
		kept := updates[:0]
		for _, update := range updates {
			if update.delete {
				st.absentDelete++
				continue
			}
			kept = append(kept, update)
		}
		return buildSorted(kept, st)
	}

	pivot := sort.Search(len(updates), func(i int) bool {
		return updates[i].key >= n.key
	})
	leftUpdates := updates[:pivot]
	rightStart := pivot
	var matched *kvUpdate
	if pivot < len(updates) && updates[pivot].key == n.key {
		matched = &updates[pivot]
		rightStart = pivot + 1
	}
	rightUpdates := updates[rightStart:]

	left := n.left
	if len(leftUpdates) > 0 {
		left = applyBatch(left, leftUpdates, st)
	}
	right := n.right
	if len(rightUpdates) > 0 {
		right = applyBatch(right, rightUpdates, st)
	}

	if matched == nil {
		if left == n.left && right == n.right {
			return n
		}
		return rebalanceNode(n, left, right, st)
	}

	if matched.delete {
		st.presentDelete++
		return mergeTrees(left, right, st)
	}

	if left == n.left && right == n.right && bytes.Equal(n.value, matched.value) {
		return n
	}

	if left == n.left && right == n.right {
		c := copyNode(n, st)
		c.value = matched.value
		return c
	}

	c := copyNode(n, st)
	c.value = matched.value
	c.left = left
	c.right = right
	refresh(c)
	return c
}

func buildSorted(updates []kvUpdate, st *batchStats) *node {
	if len(updates) == 0 {
		return nil
	}
	mid := len(updates) / 2
	left := buildSorted(updates[:mid], st)
	right := buildSorted(updates[mid+1:], st)
	return newNode(updates[mid].key, updates[mid].value, left, right, st)
}

func mergeTrees(left, right *node, st *batchStats) *node {
	if left == nil {
		return right
	}
	if right == nil {
		return left
	}

	if nodeHeight(left) > nodeHeight(right)+1 {
		mergedRight := mergeTrees(left.right, right, st)
		return rebalanceNode(left, left.left, mergedRight, st)
	}
	if nodeHeight(right) > nodeHeight(left)+1 {
		mergedLeft := mergeTrees(left, right.left, st)
		return rebalanceNode(right, mergedLeft, right.right, st)
	}

	successor, newRight := removeMin(right, st)
	return rebalanceNode(successor, left, newRight, st)
}

func removeMin(n *node, st *batchStats) (*node, *node) {
	if n.left == nil {
		return n, n.right
	}
	minNode, newLeft := removeMin(n.left, st)
	return minNode, rebalanceNode(n, newLeft, n.right, st)
}

func newNode(key string, value []byte, left, right *node, st *batchStats) *node {
	n := &node{
		key:   key,
		value: value,
		left:  left,
		right: right,
	}
	refresh(n)
	if st != nil {
		st.built++
	}
	return n
}

func copyNode(src *node, st *batchStats) *node {
	c := *src
	if st != nil {
		st.copied++
	}
	return &c
}

func rebalanceNode(old, left, right *node, st *batchStats) *node {
	factor := int(nodeHeight(left)) - int(nodeHeight(right))
	switch {
	case factor > 1:
		if balanceFactor(left) >= 0 {
			return rotateRight(old, left, st)
		}
		return rotateLeftRight(old, left, left.right, st)
	case factor < -1:
		if balanceFactor(right) <= 0 {
			return rotateLeft(old, right, st)
		}
		return rotateRightLeft(old, right, right.left, st)
	default:
		c := copyNode(old, st)
		c.left = left
		c.right = right
		refresh(c)
		return c
	}
}

func rotateRight(root, left *node, st *batchStats) *node {
	newRoot := copyNode(root, st)
	newRoot.left = left.right
	refresh(newRoot)

	newLeft := copyNode(left, st)
	newLeft.right = newRoot
	refresh(newLeft)
	return newLeft
}

func rotateLeft(root, right *node, st *batchStats) *node {
	newRoot := copyNode(root, st)
	newRoot.right = right.left
	refresh(newRoot)

	newRight := copyNode(right, st)
	newRight.left = newRoot
	refresh(newRight)
	return newRight
}

func rotateLeftRight(root, left, middle *node, st *batchStats) *node {
	newLeft := copyNode(left, st)
	newLeft.right = middle.left
	refresh(newLeft)

	newRight := copyNode(root, st)
	newRight.left = middle.right
	refresh(newRight)

	newMiddle := copyNode(middle, st)
	newMiddle.left = newLeft
	newMiddle.right = newRight
	refresh(newMiddle)
	return newMiddle
}

func rotateRightLeft(root, right, middle *node, st *batchStats) *node {
	newLeft := copyNode(root, st)
	newLeft.right = middle.left
	refresh(newLeft)

	newRight := copyNode(right, st)
	newRight.left = middle.right
	refresh(newRight)

	newMiddle := copyNode(middle, st)
	newMiddle.left = newLeft
	newMiddle.right = newRight
	refresh(newMiddle)
	return newMiddle
}

func refresh(n *node) {
	n.size = nodeSize(n.left) + nodeSize(n.right) + 1
	height := nodeHeight(n.left)
	if nodeHeight(n.right) > height {
		height = nodeHeight(n.right)
	}
	n.height = height + 1
}
