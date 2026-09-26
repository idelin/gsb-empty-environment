// Package pct implements a persistent (immutable) AVL tree using path copying.
//
// Every update returns a new root; old roots keep working unchanged because
// nodes are never mutated in place. Unchanged subtrees are shared between
// versions by pointer, so a single Put/Delete copies only O(log n) nodes and
// a BatchPut of m sorted keys copies O(m log(n/m)) nodes instead of
// m independent O(log n) paths.
package pct

import (
	"encoding/binary"
	"hash/fnv"
	"sort"
	"unsafe"
)

// node is the single allocation unit of the tree.
//
// Memory layout on 64-bit platforms (unsafe.Sizeof(node{}) == 40):
//
//	offset  0: key    int64   8 bytes
//	offset  8: val    int64   8 bytes
//	offset 16: height int32   4 bytes
//	offset 20: size   int32   4 bytes  (subtree count, maintained through rotations)
//	offset 24: left   *node   8 bytes
//	offset 32: right  *node   8 bytes
//
// No padding is needed; fields are ordered largest-alignment first.
// Go adds no per-object header, but the runtime allocator rounds 40 bytes
// up to the 48-byte size class, so each node costs 48 bytes of heap.
type node struct {
	key    int64
	val    int64
	height int32
	size   int32
	left   *node
	right  *node
}

// KV is one key/value pair, used by BatchPut.
type KV struct {
	Key int64
	Val int64
}

// Tree is an immutable handle to one version of the map. The zero value is
// an empty tree. Tree values are safe to copy; they are just root pointers.
type Tree struct {
	root *node
}

func sizeOf(n *node) int32 {
	if n == nil {
		return 0
	}
	return n.size
}

func heightOf(n *node) int32 {
	if n == nil {
		return 0
	}
	return n.height
}

// mk builds a fresh node with derived fields recomputed from its children.
func mk(key, val int64, l, r *node) *node {
	n := &node{key: key, val: val, left: l, right: r}
	n.height = 1 + max(heightOf(l), heightOf(r))
	n.size = 1 + sizeOf(l) + sizeOf(r)
	return n
}

func rebalance(n *node) *node {
	bf := heightOf(n.left) - heightOf(n.right)
	if bf > 1 {
		// Left heavy. Path copying: both rotations allocate new nodes and
		// recompute height/size bottom-up, so subtree counts stay exact.
		if heightOf(n.left.left) < heightOf(n.left.right) {
			return rotateRight(mk(n.key, n.val, rotateLeft(n.left), n.right))
		}
		return rotateRight(n)
	}
	if bf < -1 {
		if heightOf(n.right.right) < heightOf(n.right.left) {
			return rotateLeft(mk(n.key, n.val, n.left, rotateRight(n.right)))
		}
		return rotateLeft(n)
	}
	return n
}

func rotateLeft(n *node) *node {
	r := n.right
	return mk(r.key, r.val, mk(n.key, n.val, n.left, r.left), r.right)
}

func rotateRight(n *node) *node {
	l := n.left
	return mk(l.key, l.val, l.left, mk(n.key, n.val, l.right, n.right))
}

// Len returns the number of keys in this version.
func (t Tree) Len() int { return int(sizeOf(t.root)) }

// Get returns the value stored under key.
func (t Tree) Get(key int64) (int64, bool) {
	for n := t.root; n != nil; {
		switch {
		case key < n.key:
			n = n.left
		case key > n.key:
			n = n.right
		default:
			return n.val, true
		}
	}
	return 0, false
}

// Put returns a new version with key set to val. The receiver is unchanged.
func (t Tree) Put(key, val int64) Tree {
	return Tree{root: put(t.root, key, val)}
}

func put(n *node, key, val int64) *node {
	if n == nil {
		return &node{key: key, val: val, height: 1, size: 1}
	}
	switch {
	case key < n.key:
		return rebalance(mk(n.key, n.val, put(n.left, key, val), n.right))
	case key > n.key:
		return rebalance(mk(n.key, n.val, n.left, put(n.right, key, val)))
	default:
		if n.val == val {
			return n // no-op: share the whole subtree
		}
		return mk(key, val, n.left, n.right)
	}
}

// Delete returns a new version without key. If key is absent the same root
// pointer is returned, so the version (and its Digest) is bit-identical.
func (t Tree) Delete(key int64) Tree {
	root, _ := del(t.root, key)
	return Tree{root: root}
}

func del(n *node, key int64) (*node, bool) {
	if n == nil {
		return nil, false
	}
	switch {
	case key < n.key:
		l, ok := del(n.left, key)
		if !ok {
			return n, false // untouched: keep sharing this subtree
		}
		return rebalance(mk(n.key, n.val, l, n.right)), true
	case key > n.key:
		r, ok := del(n.right, key)
		if !ok {
			return n, false
		}
		return rebalance(mk(n.key, n.val, n.left, r)), true
	}
	if n.left == nil {
		return n.right, true
	}
	if n.right == nil {
		return n.left, true
	}
	r, succ := delMin(n.right)
	return rebalance(mk(succ.key, succ.val, n.left, r)), true
}

func delMin(n *node) (newSub, min *node) {
	if n.left == nil {
		return n.right, n
	}
	l, min := delMin(n.left)
	return rebalance(mk(n.key, n.val, l, n.right)), min
}

// BatchPut applies all kvs in one pass and returns the new version.
//
// The slice is sorted and de-duplicated (last write wins), then applied with
// a single divide-and-conquer sweep: at every node the update range is split
// around the node's key, and any subtree whose key range intersects no update
// is reused by pointer without being copied. This shares whole paths between
// versions instead of re-walking a root-to-leaf path per key.
func (t Tree) BatchPut(kvs []KV) Tree {
	if len(kvs) == 0 {
		return t
	}
	sorted := make([]KV, len(kvs))
	copy(sorted, kvs)
	// Stable sort so that among duplicate keys the last one in the
	// caller-provided order is the one that survives de-duplication.
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Key < sorted[j].Key })
	dedup := sorted[:1]
	for _, kv := range sorted[1:] {
		if kv.Key == dedup[len(dedup)-1].Key {
			dedup[len(dedup)-1].Val = kv.Val // last write wins
		} else {
			dedup = append(dedup, kv)
		}
	}
	return Tree{root: batchPut(t.root, dedup)}
}

func batchPut(n *node, kvs []KV) *node {
	if len(kvs) == 0 {
		return n // entire subtree shared, zero copies
	}
	if n == nil {
		return buildBalanced(kvs)
	}
	// Split updates around n.key. After de-duplication at most one equals it.
	lo := sort.Search(len(kvs), func(i int) bool { return kvs[i].Key >= n.key })
	hi := lo
	val := n.val
	if hi < len(kvs) && kvs[hi].Key == n.key {
		val = kvs[hi].Val
		hi++
	}
	left := batchPut(n.left, kvs[:lo])
	right := batchPut(n.right, kvs[hi:])
	if left == n.left && right == n.right && val == n.val {
		return n
	}
	return rebalance(mk(n.key, val, left, right))
}

// buildBalanced packs a sorted slice into a perfectly balanced tree in O(m).
func buildBalanced(kvs []KV) *node {
	if len(kvs) == 0 {
		return nil
	}
	mid := len(kvs) / 2
	return mk(kvs[mid].Key, kvs[mid].Val, buildBalanced(kvs[:mid]), buildBalanced(kvs[mid+1:]))
}

// Rank returns the number of keys strictly less than key (order statistic).
func (t Tree) Rank(key int64) int {
	rank := 0
	for n := t.root; n != nil; {
		if key <= n.key {
			n = n.left
		} else {
			rank += int(sizeOf(n.left)) + 1
			n = n.right
		}
	}
	return rank
}

// Select returns the i-th smallest key (0-based).
func (t Tree) Select(i int) (int64, bool) {
	if i < 0 || i >= int(sizeOf(t.root)) {
		return 0, false
	}
	n := t.root
	for {
		ls := int(sizeOf(n.left))
		switch {
		case i < ls:
			n = n.left
		case i == ls:
			return n.key, true
		default:
			i -= ls + 1
			n = n.right
		}
	}
}

// Iterator walks a version's keys in ascending order. It holds only a stack
// of immutable node pointers, so iterators from different versions never
// interfere with each other or with later updates.
type Iterator struct {
	stack   []*node
	hi      int64
	started bool
}

// Iterate returns an iterator over all keys.
func (t Tree) Iterate() *Iterator { return t.RangeFrom(-1 << 63) }

// Range returns an iterator over keys in [lo, hi].
func (t Tree) Range(lo, hi int64) *Iterator {
	it := &Iterator{hi: hi}
	it.descend(t.root, lo)
	return it
}

// RangeFrom returns an iterator over all keys >= lo (unbounded above).
func (t Tree) RangeFrom(lo int64) *Iterator {
	return t.Range(lo, 1<<63-1)
}

// descend pushes the left spine of nodes with key >= lo.
func (it *Iterator) descend(n *node, lo int64) {
	for n != nil {
		if n.key < lo {
			n = n.right
		} else {
			it.stack = append(it.stack, n)
			n = n.left
		}
	}
}

// Next advances the iterator; call Key/Value only when it returns true.
func (it *Iterator) Next() bool {
	if it.started {
		top := it.stack[len(it.stack)-1]
		it.stack = it.stack[:len(it.stack)-1]
		it.descend(top.right, top.key+1)
	}
	it.started = true
	return len(it.stack) > 0 && it.stack[len(it.stack)-1].key <= it.hi
}

// Key returns the current key. Valid after Next returned true.
func (it *Iterator) Key() int64 { return it.stack[len(it.stack)-1].key }

// Value returns the current value. Valid after Next returned true.
func (it *Iterator) Value() int64 { return it.stack[len(it.stack)-1].val }

// Digest returns a 64-bit FNV-1a hash over the in-order (key, value, height)
// stream. Two versions with equal logical content and shape share a digest;
// deleting an absent key returns the identical root, hence identical digest.
func (t Tree) Digest() uint64 {
	h := fnv.New64a()
	var buf [16]byte
	var walk func(n *node)
	walk = func(n *node) {
		if n == nil {
			return
		}
		walk(n.left)
		binary.LittleEndian.PutUint64(buf[0:8], uint64(n.key))
		binary.LittleEndian.PutUint64(buf[8:16], uint64(n.val))
		h.Write(buf[:])
		h.Write([]byte{byte(n.height)})
		walk(n.right)
	}
	walk(t.root)
	return h.Sum64()
}

// Stats reports real memory occupancy of this version.
type Stats struct {
	Nodes        int // live nodes reachable from this root
	BytesPerNode int // unsafe.Sizeof(node{}), logical payload
	TotalBytes   int // Nodes * BytesPerNode (excludes allocator rounding)
	Height       int
}

// NodeBytes is the exact in-memory size of one node on this platform.
const NodeBytes = int(unsafe.Sizeof(node{}))

func (t Tree) Stats() Stats {
	n := int(sizeOf(t.root))
	return Stats{
		Nodes:        n,
		BytesPerNode: NodeBytes,
		TotalBytes:   n * NodeBytes,
		Height:       int(heightOf(t.root)),
	}
}

// SharedNodes counts how many nodes two versions share by pointer identity.
// It is exported for tooling and tests; it runs in O(min size of the trees).
func SharedNodes(a, b Tree) int {
	seen := make(map[*node]struct{})
	var mark func(n *node)
	mark = func(n *node) {
		if n == nil {
			return
		}
		seen[n] = struct{}{}
		mark(n.left)
		mark(n.right)
	}
	mark(a.root)
	count := 0
	var walk func(n *node)
	walk = func(n *node) {
		if n == nil {
			return
		}
		if _, ok := seen[n]; ok {
			count += int(sizeOf(n)) // whole shared subtree, stop descending
			return
		}
		walk(n.left)
		walk(n.right)
	}
	walk(b.root)
	return count
}
