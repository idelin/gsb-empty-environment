// Package blink implements a concurrent Lehman-Yao B-link tree.
//
// Every page carries a high key and a right link. A search that observes a
// key greater than a page's high key follows the right link and retries on the
// next page. Readers hold at most one page lock at a time, and writers publish
// both halves of a split before walking upward to install the new separator.
// Each split keeps per-page split latches until its parent pointer is installed;
// unrelated subtrees continue splitting in parallel.
package blink

import (
	"bytes"
	"errors"
	"sort"
	"sync"
	"sync/atomic"
)

const (
	// DefaultMaxKeys is the default leaf capacity.
	DefaultMaxKeys = 64

	// DefaultMaxChildren is the default internal page fan-out.
	DefaultMaxChildren = 64
)

// ErrInvalidRange is returned when the lower bound of a range is greater than
// its upper bound.
var ErrInvalidRange = errors.New("blink: invalid range")

// Entry is one key/value pair returned by a range scan.
type Entry struct {
	Key   []byte
	Value []byte
}

// Pair is an alias for Entry.
type Pair = Entry

// Options configures a Tree. The zero value is valid.
type Options struct {
	// MaxKeys is the maximum number of leaf entries. Values below 2 are
	// raised to 2.
	MaxKeys int

	// MaxChildren is the maximum number of internal-page children. Values
	// below 2 are raised to 2, and the default is MaxKeys.
	MaxChildren int

	// AfterSplit, when non-nil, runs after a page split has reached its safe
	// state and the split page's write lock has been released, but before the
	// new right sibling is inserted into the parent. It is useful for testing
	// the move-right protocol and for deliberately delaying the parent update.
	// The split pages' per-page split latches are held, but no page data lock
	// is held. The callback must be read-only; in particular it must not start
	// another split.
	AfterSplit func()
}

// Stats is a point-in-time snapshot of tree counters and shape.
type Stats struct {
	Len         int64
	Pages       uint64
	Height      int
	RootPageID  uint64
	Inserts     uint64
	Updates     uint64
	Deletes     uint64
	Gets        uint64
	Splits      uint64
	RootSplits  uint64
	Reclaims    uint64
	MoveRights  uint64
	MaxKeys     int
	MaxChildren int
}

type node struct {
	mu sync.RWMutex

	id   uint64
	leaf bool

	// highKey is inclusive. nil means positive infinity. It is immutable
	// between splits and is protected by mu.
	highKey []byte

	// rightLink is protected by mu. It is non-nil exactly while the page has a
	// right sibling, including the transient period before the parent learns
	// about the sibling.
	rightLink *node

	// keys and values are used by leaves. The key order is strictly
	// increasing.
	keys   [][]byte
	values [][]byte

	// children and bounds are used by internal pages. bounds[i] is the
	// inclusive upper-bound snapshot for children[i]. A bound may be wider
	// than the child's current highKey while a child split is being propagated
	// upward. nil means positive infinity.
	children []*node
	bounds   [][]byte

	// dead is set for a reclaimed empty leaf. A thread that has a stale
	// pointer to a dead page restarts from the root.
	dead bool

	// splitMu is held from before a page is split until the resulting right
	// sibling has been inserted into the parent. It prevents a page from
	// splitting twice before the first split is visible to the level above.
	splitMu sync.Mutex
}

// Tree is a concurrent page-locked B-link tree. Its methods are safe for
// concurrent use.
type Tree struct {
	root atomic.Pointer[node]

	maxKeys     int
	maxChildren int
	afterSplit  func()

	nextPageID atomic.Uint64
	reclaimMu  sync.Mutex

	count      atomic.Int64
	pages      atomic.Uint64
	inserts    atomic.Uint64
	updates    atomic.Uint64
	deletes    atomic.Uint64
	gets       atomic.Uint64
	splits     atomic.Uint64
	rootSplits atomic.Uint64
	reclaims   atomic.Uint64
	moveRights atomic.Uint64
}

// Index is an alias for Tree.
type Index = Tree

// New returns an empty tree. If maxKeys is supplied, it configures the leaf
// capacity; otherwise DefaultMaxKeys is used.
func New(maxKeys ...int) *Tree {
	opts := Options{MaxKeys: DefaultMaxKeys}
	if len(maxKeys) > 0 {
		opts.MaxKeys = maxKeys[0]
	}
	return NewWithOptions(opts)
}

// NewWithOptions returns an empty tree with the supplied options.
func NewWithOptions(opts Options) *Tree {
	maxKeys := opts.MaxKeys
	if maxKeys == 0 {
		maxKeys = DefaultMaxKeys
	}
	if maxKeys < 2 {
		maxKeys = 2
	}
	maxChildren := opts.MaxChildren
	if maxChildren == 0 {
		maxChildren = maxKeys
	}
	if maxChildren < 2 {
		maxChildren = 2
	}

	t := &Tree{
		maxKeys:     maxKeys,
		maxChildren: maxChildren,
		afterSplit:  opts.AfterSplit,
	}
	root := t.allocPage(true, nil)
	t.root.Store(root)
	return t
}

func (t *Tree) allocPage(leaf bool, highKey []byte) *node {
	n := &node{
		id:      t.nextPageID.Add(1),
		leaf:    leaf,
		highKey: cloneBound(highKey),
	}
	t.pages.Add(1)
	return n
}

// Insert adds a key/value pair. It returns true when the key did not already
// exist and false when an existing value was replaced.
func (t *Tree) Insert(key, value []byte) bool {
	k := cloneKey(key)
	v := cloneValue(value)

	for {
		leaf := t.lockLeafWrite(k)
		if leaf == nil {
			continue
		}

		idx, found := searchLeaf(leaf.keys, k)
		if found {
			leaf.values[idx] = v
			leaf.mu.Unlock()
			t.updates.Add(1)
			return false
		}

		leaf.keys = insertKeyAt(leaf.keys, idx, k)
		leaf.values = insertValueAt(leaf.values, idx, v)
		t.count.Add(1)
		t.inserts.Add(1)

		if len(leaf.keys) <= t.maxKeys {
			leaf.mu.Unlock()
			return true
		}
		leaf.mu.Unlock()
		return t.splitLeafAndInstall(leaf)
	}
}

// splitLeafAndInstall holds the leaf's split latch until the new right sibling
// has been published in its parent. Other leaves remain fully writable.
func (t *Tree) splitLeafAndInstall(leaf *node) bool {
	leaf.splitMu.Lock()
	defer leaf.splitMu.Unlock()

	leaf.mu.Lock()
	if leaf.dead || len(leaf.keys) <= t.maxKeys {
		leaf.mu.Unlock()
		return true
	}

	sep, oldHigh, right := t.splitLeafLocked(leaf)
	right.splitMu.Lock()
	leaf.mu.Unlock()
	defer right.splitMu.Unlock()
	t.splits.Add(1)
	t.runAfterSplit()
	t.installSplit(leaf, right, sep, oldHigh)
	return true
}

// Put is an alias for Insert.
func (t *Tree) Put(key, value []byte) bool {
	return t.Insert(key, value)
}

// Store is an alias for Insert.
func (t *Tree) Store(key, value []byte) bool {
	return t.Insert(key, value)
}

// Get returns a copy of the value stored for key.
func (t *Tree) Get(key []byte) ([]byte, bool) {
	k := cloneKey(key)
	for {
		leaf := t.lockLeafRead(k)
		if leaf == nil {
			continue
		}
		idx, found := searchLeaf(leaf.keys, k)
		if !found {
			leaf.mu.RUnlock()
			t.gets.Add(1)
			return nil, false
		}
		value := cloneValue(leaf.values[idx])
		leaf.mu.RUnlock()
		t.gets.Add(1)
		return value, true
	}
}

// Lookup is an alias for Get.
func (t *Tree) Lookup(key []byte) ([]byte, bool) {
	return t.Get(key)
}

// Contains reports whether key is present.
func (t *Tree) Contains(key []byte) bool {
	_, ok := t.Get(key)
	return ok
}

// Delete removes key and returns the previous value. Empty leaves are detached
// lazily when their neighbours make that safe.
func (t *Tree) Delete(key []byte) ([]byte, bool) {
	k := cloneKey(key)
	for {
		leaf := t.lockLeafWrite(k)
		if leaf == nil {
			continue
		}

		idx, found := searchLeaf(leaf.keys, k)
		if !found {
			leaf.mu.Unlock()
			return nil, false
		}

		old := cloneValue(leaf.values[idx])
		leaf.keys = removeKeyAt(leaf.keys, idx)
		leaf.values = removeValueAt(leaf.values, idx)
		empty := len(leaf.keys) == 0
		leaf.mu.Unlock()

		t.count.Add(-1)
		t.deletes.Add(1)
		if empty {
			t.reclaimEmptyLeaf(leaf)
		}
		return old, true
	}
}

// Remove is an alias for Delete.
func (t *Tree) Remove(key []byte) ([]byte, bool) {
	return t.Delete(key)
}

// DeleteKey removes key and reports whether it was present.
func (t *Tree) DeleteKey(key []byte) bool {
	_, ok := t.Delete(key)
	return ok
}

// Close releases no external resources; it is provided for callers that manage
// indexes through a Close-based lifecycle.
func (t *Tree) Close() error {
	return nil
}

// Len returns the number of indexed keys.
func (t *Tree) Len() int {
	return int(t.count.Load())
}

// Count returns the number of indexed keys.
func (t *Tree) Count() int64 {
	return t.count.Load()
}

// RootPageID returns the current root page identifier.
func (t *Tree) RootPageID() uint64 {
	root := t.root.Load()
	if root == nil {
		return 0
	}
	return root.id
}

// Stats returns a snapshot of counters and tree shape.
func (t *Tree) Stats() Stats {
	return Stats{
		Len:         t.count.Load(),
		Pages:       t.pages.Load(),
		Height:      t.Height(),
		RootPageID:  t.RootPageID(),
		Inserts:     t.inserts.Load(),
		Updates:     t.updates.Load(),
		Deletes:     t.deletes.Load(),
		Gets:        t.gets.Load(),
		Splits:      t.splits.Load(),
		RootSplits:  t.rootSplits.Load(),
		Reclaims:    t.reclaims.Load(),
		MoveRights:  t.moveRights.Load(),
		MaxKeys:     t.maxKeys,
		MaxChildren: t.maxChildren,
	}
}

func (t *Tree) runAfterSplit() {
	if t.afterSplit != nil {
		t.afterSplit()
	}
}

func (t *Tree) splitLeafLocked(left *node) (sep, oldHigh []byte, right *node) {
	mid := len(left.keys) / 2
	sep = left.keys[mid-1]
	oldHigh = left.highKey

	right = t.allocPage(true, oldHigh)
	right.keys = left.keys[mid:]
	right.values = left.values[mid:]
	right.rightLink = left.rightLink

	left.keys = left.keys[:mid:mid]
	left.values = left.values[:mid:mid]
	left.highKey = sep
	left.rightLink = right
	return sep, oldHigh, right
}

func (t *Tree) splitInternalLocked(left *node) (sep, oldHigh []byte, right *node) {
	mid := len(left.children) / 2
	sep = left.bounds[mid]
	oldHigh = left.highKey

	right = t.allocPage(false, oldHigh)
	right.children = append([]*node(nil), left.children[mid+1:]...)
	right.bounds = append([][]byte(nil), left.bounds[mid+1:]...)
	right.rightLink = left.rightLink

	left.children = left.children[: mid+1 : mid+1]
	left.bounds = left.bounds[: mid+1 : mid+1]
	left.highKey = sep
	left.rightLink = right
	return sep, oldHigh, right
}

// installSplit walks upward without retaining page data locks. A parent split
// from another branch is handled by move-right during parent discovery; the
// separator key also determines the correct insertion point in the parent.
func (t *Tree) installSplit(left, right *node, sep, oldHigh []byte) {
	for {
		parent := t.findParent(left, sep)
		if parent == nil {
			if t.installRoot(left, right, sep, oldHigh) {
				return
			}
			continue
		}

		parent.mu.Lock()
		if parent.dead || parent.leaf {
			parent.mu.Unlock()
			continue
		}

		leftIdx := indexChild(parent.children, left)
		if leftIdx < 0 {
			parent.mu.Unlock()
			continue
		}
		if indexChild(parent.children, right) >= 0 {
			parent.mu.Unlock()
			return
		}

		ins := insertionPosition(parent, leftIdx, sep)
		if bound := parent.bounds[leftIdx]; bound == nil || bytes.Compare(bound, sep) > 0 {
			parent.bounds[leftIdx] = sep
		}

		parent.children = insertNodeAt(parent.children, ins, right)
		parent.bounds = insertBoundAt(parent.bounds, ins, oldHigh)

		if len(parent.children) <= t.maxChildren {
			parent.mu.Unlock()
			return
		}
		parent.mu.Unlock()
		t.splitInternalAndInstall(parent)
		return
	}
}

func (t *Tree) splitInternalAndInstall(page *node) {
	page.splitMu.Lock()
	defer page.splitMu.Unlock()

	page.mu.Lock()
	if page.dead || page.leaf || len(page.children) <= t.maxChildren {
		page.mu.Unlock()
		return
	}

	sep, oldHigh, right := t.splitInternalLocked(page)
	right.splitMu.Lock()
	page.mu.Unlock()
	defer right.splitMu.Unlock()

	t.splits.Add(1)
	t.runAfterSplit()
	t.installSplit(page, right, sep, oldHigh)
}

func (t *Tree) installRoot(left, right *node, sep, oldHigh []byte) bool {
	if t.root.Load() != left {
		return false
	}

	root := t.allocPage(false, nil)
	root.children = []*node{left, right}
	root.bounds = [][]byte{sep, oldHigh}

	if !t.root.CompareAndSwap(left, root) {
		t.pages.Add(^uint64(0))
		return false
	}
	t.rootSplits.Add(1)
	return true
}

// findParent returns the current direct parent of target, or nil when target is
// the root. It follows right links at each level, so a parent split during the
// search is handled without locking an entire path.
func (t *Tree) findParent(target *node, key []byte) *node {
	for attempt := 0; attempt < 8; attempt++ {
		cur := t.root.Load()
		if cur == nil {
			return nil
		}
		if cur == target {
			return nil
		}

		for cur != nil && !cur.leaf {
			cur.mu.RLock()
			if indexChild(cur.children, target) >= 0 {
				cur.mu.RUnlock()
				return cur
			}

			if keyExceedsHigh(key, cur.highKey) {
				next := cur.rightLink
				cur.mu.RUnlock()
				if next == nil {
					cur = nil
					break
				}
				t.moveRights.Add(1)
				cur = next
				continue
			}

			child := routeChild(cur, key)
			cur.mu.RUnlock()
			if child == nil {
				cur = nil
				break
			}
			cur = child
		}
	}
	return t.findParentByPointer(target)
}

// findParentByPointer is the slow but routing-independent fallback used for an
// empty rightmost page, whose nil high key cannot select a search key. It holds
// one internal page lock at a time and never reads a child's fields.
func (t *Tree) findParentByPointer(target *node) *node {
	root := t.root.Load()
	if root == nil || root == target {
		return nil
	}

	stack := []*node{root}
	seen := make(map[*node]bool)
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[cur] {
			continue
		}
		seen[cur] = true

		cur.mu.RLock()
		if cur.leaf {
			cur.mu.RUnlock()
			continue
		}
		if indexChild(cur.children, target) >= 0 {
			cur.mu.RUnlock()
			return cur
		}
		for _, child := range cur.children {
			if !child.leaf {
				stack = append(stack, child)
			}
		}
		cur.mu.RUnlock()
	}
	return nil
}

func (t *Tree) lockLeafWrite(key []byte) *node {
	for {
		n := t.descend(key)
		for {
			n.mu.Lock()
			if n.dead {
				n.mu.Unlock()
				break
			}
			if keyExceedsHigh(key, n.highKey) {
				next := n.rightLink
				n.mu.Unlock()
				if next == nil {
					break
				}
				t.moveRights.Add(1)
				n = next
				continue
			}
			return n
		}
	}
}

func (t *Tree) lockLeafRead(key []byte) *node {
	for {
		n := t.descend(key)
		for {
			n.mu.RLock()
			if n.dead {
				n.mu.RUnlock()
				break
			}
			if keyExceedsHigh(key, n.highKey) {
				next := n.rightLink
				n.mu.RUnlock()
				if next == nil {
					break
				}
				t.moveRights.Add(1)
				n = next
				continue
			}
			return n
		}
	}
}

func (t *Tree) descend(key []byte) *node {
	n := t.root.Load()
	for n != nil && !n.leaf {
		n.mu.RLock()
		if keyExceedsHigh(key, n.highKey) {
			next := n.rightLink
			n.mu.RUnlock()
			if next == nil {
				return nil
			}
			t.moveRights.Add(1)
			n = next
			continue
		}
		child := routeChild(n, key)
		n.mu.RUnlock()
		n = child
	}
	return n
}

func (t *Tree) descendLeftmost() *node {
	n := t.root.Load()
	for n != nil && !n.leaf {
		n.mu.RLock()
		var child *node
		if len(n.children) > 0 {
			child = n.children[0]
		}
		n.mu.RUnlock()
		n = child
	}
	return n
}

func (t *Tree) reclaimEmptyLeaf(leaf *node) {
	t.reclaimMu.Lock()
	defer t.reclaimMu.Unlock()

	leaf.mu.Lock()
	defer leaf.mu.Unlock()
	if leaf.dead || !leaf.leaf || len(leaf.keys) != 0 {
		return
	}

	high := leaf.highKey
	next := leaf.rightLink

	for {
		parent := t.findParent(leaf, high)
		if parent == nil {
			return
		}

		parent.mu.Lock()
		if parent.dead || parent.leaf {
			parent.mu.Unlock()
			continue
		}

		idx := indexChild(parent.children, leaf)
		if idx < 0 {
			parent.mu.Unlock()
			continue
		}

		// Without a left sibling there is no page to inherit this page's key
		// range. With a right neighbour not yet represented in this parent,
		// reclaiming would strand keys that still live in that neighbour.
		if idx == 0 {
			parent.mu.Unlock()
			return
		}
		if next != nil {
			if idx+1 >= len(parent.children) || parent.children[idx+1] != next {
				parent.mu.Unlock()
				return
			}
		}

		pred := parent.children[idx-1]
		pred.mu.Lock()
		if pred.dead || !pred.leaf {
			pred.mu.Unlock()
			parent.mu.Unlock()
			return
		}

		pred.highKey = high
		pred.rightLink = next
		parent.bounds[idx-1] = high
		parent.children = removeNodeAt(parent.children, idx)
		parent.bounds = removeBoundAt(parent.bounds, idx)
		leaf.dead = true

		pred.mu.Unlock()
		parent.mu.Unlock()
		t.reclaims.Add(1)
		return
	}
}

// ReclaimEmpty walks the leaf chain and attempts to detach empty leaves. It is
// safe to call concurrently with other operations and is mostly useful after a
// burst of deletes.
func (t *Tree) ReclaimEmpty() int {
	start := t.descendLeftmost()
	reclaimed := 0
	for start != nil {
		start.mu.RLock()
		if start.dead {
			next := start.rightLink
			start.mu.RUnlock()
			start = next
			continue
		}
		empty := start.leaf && len(start.keys) == 0
		next := start.rightLink
		start.mu.RUnlock()

		if empty {
			before := t.reclaims.Load()
			t.reclaimEmptyLeaf(start)
			if t.reclaims.Load() > before {
				reclaimed++
			}
		}
		start = next
	}
	return reclaimed
}

// Height returns the current tree height in pages. The root is height one.
func (t *Tree) Height() int {
	n := t.root.Load()
	height := 0
	for n != nil {
		height++
		if n.leaf {
			break
		}
		n.mu.RLock()
		var child *node
		if len(n.children) > 0 {
			child = n.children[0]
		}
		n.mu.RUnlock()
		n = child
	}
	return height
}

func searchLeaf(keys [][]byte, key []byte) (int, bool) {
	idx := sort.Search(len(keys), func(i int) bool {
		return bytes.Compare(keys[i], key) >= 0
	})
	return idx, idx < len(keys) && bytes.Equal(keys[idx], key)
}

func routeChild(n *node, key []byte) *node {
	if len(n.children) == 0 {
		return nil
	}
	for i, bound := range n.bounds {
		if bound == nil || bytes.Compare(key, bound) <= 0 {
			return n.children[i]
		}
	}
	return n.children[len(n.children)-1]
}

// insertionPosition returns the index at which the new right sibling must be
// inserted. Inserting at the first bound strictly greater than sep preserves
// bound order for both the common case and concurrent insertions from sibling
// branches.
func insertionPosition(n *node, leftIdx int, sep []byte) int {
	ins := leftIdx + 1
	for ins < len(n.bounds) {
		bound := n.bounds[ins]
		if bound == nil || bytes.Compare(bound, sep) > 0 {
			break
		}
		ins++
	}
	return ins
}

func keyExceedsHigh(key, high []byte) bool {
	return high != nil && bytes.Compare(key, high) > 0
}

func indexChild(children []*node, target *node) int {
	for i, child := range children {
		if child == target {
			return i
		}
	}
	return -1
}

func insertKeyAt(keys [][]byte, idx int, key []byte) [][]byte {
	keys = append(keys, nil)
	copy(keys[idx+1:], keys[idx:])
	keys[idx] = key
	return keys
}

func insertValueAt(values [][]byte, idx int, value []byte) [][]byte {
	values = append(values, nil)
	copy(values[idx+1:], values[idx:])
	values[idx] = value
	return values
}

func removeKeyAt(keys [][]byte, idx int) [][]byte {
	copy(keys[idx:], keys[idx+1:])
	keys[len(keys)-1] = nil
	return keys[:len(keys)-1]
}

func removeValueAt(values [][]byte, idx int) [][]byte {
	copy(values[idx:], values[idx+1:])
	values[len(values)-1] = nil
	return values[:len(values)-1]
}

func insertNodeAt(nodes []*node, idx int, value *node) []*node {
	nodes = append(nodes, nil)
	copy(nodes[idx+1:], nodes[idx:])
	nodes[idx] = value
	return nodes
}

func insertBoundAt(bounds [][]byte, idx int, value []byte) [][]byte {
	bounds = append(bounds, nil)
	copy(bounds[idx+1:], bounds[idx:])
	bounds[idx] = value
	return bounds
}

func removeNodeAt(nodes []*node, idx int) []*node {
	copy(nodes[idx:], nodes[idx+1:])
	nodes[len(nodes)-1] = nil
	return nodes[:len(nodes)-1]
}

func removeBoundAt(bounds [][]byte, idx int) [][]byte {
	copy(bounds[idx:], bounds[idx+1:])
	bounds[len(bounds)-1] = nil
	return bounds[:len(bounds)-1]
}

func cloneKey(key []byte) []byte {
	if key == nil {
		return []byte{}
	}
	return bytes.Clone(key)
}

func cloneValue(value []byte) []byte {
	if value == nil {
		return nil
	}
	return bytes.Clone(value)
}

func cloneBound(bound []byte) []byte {
	if bound == nil {
		return nil
	}
	return bytes.Clone(bound)
}
