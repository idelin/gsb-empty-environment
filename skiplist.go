// Package skiplist implements a concurrent in-memory ordered table (MemTable)
// backed by a multi-level randomized skip list.
//
// Concurrency model:
//   - Inserts link new nodes with atomic compare-and-swap on the per-level
//     forward pointers; readers always traverse with atomic loads and never
//     take locks.
//   - Every node is assigned a monotonically increasing sequence number at
//     its linearization point (after it is linked into the bottom level).
//   - Iterators capture the sequence counter at creation time and only
//     surface nodes whose sequence number is visible in that snapshot, so a
//     snapshot iterator is fully isolated from concurrent writes.
package skiplist

import (
	"math"
	"sync/atomic"
)

// maxLevel is the maximum number of forward levels a node may occupy.
// 20 levels comfortably index ~1M nodes at p=1/2.
const maxLevel = 20

type node struct {
	key   int64
	value atomic.Int64
	// seq is 0 until the node becomes visible (its linearization point).
	seq  atomic.Uint64
	next [maxLevel]atomic.Pointer[node]
}

// SkipList is a concurrent ordered map from int64 keys to int64 values.
// The zero value is not ready for use; create one with New.
type SkipList struct {
	head *node
	seq  atomic.Uint64
	rand atomic.Uint64
	size atomic.Int64
}

// New returns an empty SkipList.
func New() *SkipList {
	return &SkipList{head: &node{key: math.MinInt64}}
}

// randomLevel draws a level in [1, maxLevel] with p=1/2 geometric decay,
// using a lock-free xorshift PRNG seeded from an atomic counter.
func (s *SkipList) randomLevel() int {
	x := s.rand.Add(0x9E3779B97F4A7C15)
	x ^= x << 13
	x ^= x >> 7
	x ^= x << 17
	level := 1
	for level < maxLevel && x&1 == 1 {
		level++
		x >>= 1
	}
	return level
}

// find returns, for every level, the last node with key < target (preds)
// and the first node with key >= target (succs).
func (s *SkipList) find(key int64) (preds, succs [maxLevel]*node) {
	pred := s.head
	for i := maxLevel - 1; i >= 0; i-- {
		cur := pred.next[i].Load()
		for cur != nil && cur.key < key {
			pred = cur
			cur = pred.next[i].Load()
		}
		preds[i] = pred
		succs[i] = cur
	}
	return preds, succs
}

// Insert adds or updates key with value. It is safe for concurrent use and
// never blocks readers: nodes are spliced in with CAS on atomic pointers.
func (s *SkipList) Insert(key int64, value int64) {
	level := s.randomLevel()
	n := &node{key: key}
	n.value.Store(value)

	// Link the bottom level first; that CAS is the point at which the node
	// becomes reachable, so retry only around it.
	for {
		preds, succs := s.find(key)
		if succs[0] != nil && succs[0].key == key {
			// Key already present: update in place.
			succs[0].value.Store(value)
			return
		}
		for i := 0; i < level; i++ {
			n.next[i].Store(succs[i])
		}
		if preds[0].next[0].CompareAndSwap(succs[0], n) {
			break
		}
	}

	// Splice the upper levels. Concurrent inserts may race here, so each
	// level retries with a fresh search until its CAS succeeds.
	for i := 1; i < level; i++ {
		for {
			preds, succs := s.find(key)
			n.next[i].Store(succs[i])
			if preds[i].next[i].CompareAndSwap(succs[i], n) {
				break
			}
		}
	}

	// Linearization point for snapshot visibility: assign the sequence
	// number only after the node is fully reachable from level 0.
	n.seq.Store(s.seq.Add(1))
	s.size.Add(1)
}

// Get returns the value stored under key.
func (s *SkipList) Get(key int64) (int64, bool) {
	_, succs := s.find(key)
	if n := succs[0]; n != nil && n.key == key {
		return n.value.Load(), true
	}
	return 0, false
}

// Len returns the number of distinct keys inserted so far.
func (s *SkipList) Len() int64 {
	return s.size.Load()
}

// Iterator is a read-only snapshot cursor over a key range. It observes
// exactly the nodes whose sequence number was assigned before the iterator
// was created; concurrent inserts never leak into its output.
type Iterator struct {
	end  int64
	snap uint64
	cur  *node
}

// NewIterator returns a snapshot iterator over the inclusive range
// [start, end]. If start > end the iterator is immediately exhausted.
func (s *SkipList) NewIterator(start, end int64) *Iterator {
	it := &Iterator{end: end, snap: s.seq.Load()}
	if start > end {
		return it
	}
	_, succs := s.find(start)
	it.cur = it.firstVisible(succs[0])
	return it
}

// firstVisible advances from n to the first node that belongs to the
// snapshot and lies within the range, or nil if there is none.
func (it *Iterator) firstVisible(n *node) *node {
	for n != nil && n.key <= it.end {
		if seq := n.seq.Load(); seq != 0 && seq <= it.snap {
			return n
		}
		n = n.next[0].Load()
	}
	return nil
}

// Valid reports whether the iterator points at a live element.
func (it *Iterator) Valid() bool { return it.cur != nil }

// Key returns the current key. Only valid when Valid() is true.
func (it *Iterator) Key() int64 { return it.cur.key }

// Value returns the current value. Only valid when Valid() is true.
func (it *Iterator) Value() int64 { return it.cur.value.Load() }

// Next advances the iterator to the next visible element in the snapshot.
func (it *Iterator) Next() {
	if it.cur == nil {
		return
	}
	it.cur = it.firstVisible(it.cur.next[0].Load())
}
