// Package latchcrabbing implements a latch crabbing (lock coupling) B+ tree.
package latchcrabbing

import (
	"sort"
	"sync"
	"sync/atomic"
)

const DefaultMaxKeys = 8

// Stats exposes latch activity for demonstrations and tests.
type Stats struct {
	ReadLatches        uint64
	WriteLatches       uint64
	EarlyWriteReleases uint64
}

// Tree is a concurrent B+ tree guarded by latch crabbing.
//
// The header node is permanent and never splits. Its only child is the current
// root, which lets readers and writers discover the root without a separate
// tree-wide mutex.
type Tree struct {
	count              uint64
	readLatches        uint64
	writeLatches       uint64
	earlyWriteReleases uint64

	header  *node
	maxKeys int
}

type node struct {
	mu       sync.RWMutex
	leaf     bool
	keys     []int
	children []*node
}

// New returns an empty tree. maxKeys is the maximum number of keys held by a
// node before insertion; values below two are raised to two.
func New(maxKeys int) *Tree {
	if maxKeys < 2 {
		maxKeys = 2
	}
	root := &node{leaf: true}
	header := &node{children: []*node{root}}
	return &Tree{header: header, maxKeys: maxKeys}
}

// Contains reports whether key exists in the tree. Readers couple RLocks from
// parent to child and release each parent immediately after its child is
// latched.
func (t *Tree) Contains(key int) bool {
	t.lockRead(t.header)
	current := t.header.children[0]
	t.lockRead(current)
	t.unlockRead(t.header)

	for !current.leaf {
		child := current.children[childIndex(current, key)]
		t.lockRead(child)
		t.unlockRead(current)
		current = child
	}

	index := sort.Search(len(current.keys), func(i int) bool {
		return current.keys[i] >= key
	})
	found := index < len(current.keys) && current.keys[index] == key
	t.unlockRead(current)
	return found
}

// Insert inserts key and reports whether it was new.
//
// Writers couple WLocks downward. A node is safe for insertion when it is not
// full; once a safe child is latched, all ancestors can be released because no
// split below that child can propagate past it. Full children keep their
// ancestors latched so a bottom-up split can be installed without restarting.
func (t *Tree) Insert(key int) bool {
	t.lockWrite(t.header)
	current := t.header.children[0]
	t.lockWrite(current)

	if current.isFull(t.maxKeys) {
		right, separator := current.split()
		newRoot := &node{
			keys:     []int{separator},
			children: []*node{current, right},
		}
		t.lockWrite(newRoot)
		t.header.children[0] = newRoot
		t.unlockWrite(t.header)
		t.unlockWrite(current)
		current = newRoot
	} else {
		t.unlockWrite(t.header)
		atomic.AddUint64(&t.earlyWriteReleases, 1)
	}

	var ancestors []*node
	for !current.leaf {
		child := current.children[childIndex(current, key)]
		t.lockWrite(child)

		if child.isFull(t.maxKeys) {
			ancestors = append(ancestors, current)
		} else {
			t.unlockWrite(current)
			atomic.AddUint64(&t.earlyWriteReleases, 1)
			t.releaseAncestors(ancestors)
			ancestors = nil
		}
		current = child
	}

	inserted := current.insert(key)
	if !inserted {
		t.releaseAncestors(ancestors)
		t.unlockWrite(current)
		return false
	}

	atomic.AddUint64(&t.count, 1)
	if current.isOverflow(t.maxKeys) {
		t.propagateSplits(current, ancestors)
	} else {
		t.releaseAncestors(ancestors)
		t.unlockWrite(current)
	}
	return true
}

// Len returns the number of successful insertions.
func (t *Tree) Len() int {
	return int(atomic.LoadUint64(&t.count))
}

// Height returns the number of node levels, excluding the permanent header.
func (t *Tree) Height() int {
	t.lockRead(t.header)
	current := t.header.children[0]
	t.lockRead(current)
	t.unlockRead(t.header)

	height := 1
	for !current.leaf {
		child := current.children[0]
		t.lockRead(child)
		t.unlockRead(current)
		current = child
		height++
	}
	t.unlockRead(current)
	return height
}

// Stats returns a snapshot of latch activity.
func (t *Tree) Stats() Stats {
	return Stats{
		ReadLatches:        atomic.LoadUint64(&t.readLatches),
		WriteLatches:       atomic.LoadUint64(&t.writeLatches),
		EarlyWriteReleases: atomic.LoadUint64(&t.earlyWriteReleases),
	}
}

func (t *Tree) propagateSplits(child *node, ancestors []*node) {
	for child.isOverflow(t.maxKeys) {
		if len(ancestors) == 0 {
			panic("latchcrabbing: split reached an unlatched root")
		}

		right, separator := child.split()
		parentIndex := len(ancestors) - 1
		parent := ancestors[parentIndex]
		ancestors = ancestors[:parentIndex]

		childPosition := indexOfChild(parent, child)
		parent.insertChild(childPosition, separator, right)
		t.unlockWrite(child)
		child = parent
	}

	t.unlockWrite(child)
	t.releaseAncestors(ancestors)
}

func (t *Tree) releaseAncestors(ancestors []*node) {
	for i := len(ancestors) - 1; i >= 0; i-- {
		t.unlockWrite(ancestors[i])
	}
}

func (t *Tree) lockRead(n *node) {
	n.mu.RLock()
	atomic.AddUint64(&t.readLatches, 1)
}

func (t *Tree) unlockRead(n *node) {
	n.mu.RUnlock()
}

func (t *Tree) lockWrite(n *node) {
	n.mu.Lock()
	atomic.AddUint64(&t.writeLatches, 1)
}

func (t *Tree) unlockWrite(n *node) {
	n.mu.Unlock()
}

func (n *node) isFull(maxKeys int) bool {
	return len(n.keys) >= maxKeys
}

func (n *node) isOverflow(maxKeys int) bool {
	return len(n.keys) > maxKeys
}

func (n *node) insert(key int) bool {
	index := sort.Search(len(n.keys), func(i int) bool {
		return n.keys[i] >= key
	})
	if index < len(n.keys) && n.keys[index] == key {
		return false
	}

	n.keys = append(n.keys, 0)
	copy(n.keys[index+1:], n.keys[index:])
	n.keys[index] = key
	return true
}

func (n *node) split() (*node, int) {
	middle := len(n.keys) / 2
	right := &node{leaf: n.leaf}

	if n.leaf {
		separator := n.keys[middle]
		right.keys = append([]int(nil), n.keys[middle:]...)
		n.keys = n.keys[:middle]
		return right, separator
	}

	separator := n.keys[middle]
	right.keys = append([]int(nil), n.keys[middle+1:]...)
	right.children = append([]*node(nil), n.children[middle+1:]...)
	n.keys = n.keys[:middle]
	n.children = n.children[:middle+1]
	return right, separator
}

func (n *node) insertChild(position int, separator int, right *node) {
	n.keys = append(n.keys, 0)
	copy(n.keys[position+1:], n.keys[position:])
	n.keys[position] = separator

	n.children = append(n.children, nil)
	copy(n.children[position+2:], n.children[position+1:])
	n.children[position+1] = right
}

func childIndex(n *node, key int) int {
	return sort.Search(len(n.keys), func(i int) bool {
		return n.keys[i] > key
	})
}

func indexOfChild(parent *node, child *node) int {
	for i, candidate := range parent.children {
		if candidate == child {
			return i
		}
	}
	panic("latchcrabbing: parent does not contain split child")
}
