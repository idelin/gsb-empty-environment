package lsm

import (
	"bytes"
	"math/rand"
)

const maxLevel = 20

// slNode is a node in the skiplist. Keys and values are owned by the node.
type slNode struct {
	key  []byte
	val  []byte
	next []*slNode
}

// skiplist is an ordered in-memory map. It is not safe for concurrent use;
// callers (MemTable under the DB mutex) provide external synchronization.
type skiplist struct {
	head  *slNode
	level int
	rnd   *rand.Rand
	count int
}

func newSkiplist() *skiplist {
	return &skiplist{
		head:  &slNode{next: make([]*slNode, maxLevel)},
		level: 1,
		rnd:   rand.New(rand.NewSource(1)),
	}
}

func (s *skiplist) randomLevel() int {
	lvl := 1
	for lvl < maxLevel && s.rnd.Intn(4) == 0 {
		lvl++
	}
	return lvl
}

// Put inserts or replaces key. It reports whether an existing key was
// replaced and the length of the previous value, so the caller can keep
// approximate size accounting.
func (s *skiplist) Put(key, val []byte) (replaced bool, oldValLen int) {
	var update [maxLevel]*slNode
	cur := s.head
	for i := s.level - 1; i >= 0; i-- {
		for cur.next[i] != nil && bytes.Compare(cur.next[i].key, key) < 0 {
			cur = cur.next[i]
		}
		update[i] = cur
	}
	if next := cur.next[0]; next != nil && bytes.Equal(next.key, key) {
		oldValLen = len(next.val)
		next.val = bytes.Clone(val)
		return true, oldValLen
	}
	lvl := s.randomLevel()
	if lvl > s.level {
		for i := s.level; i < lvl; i++ {
			update[i] = s.head
		}
		s.level = lvl
	}
	n := &slNode{key: bytes.Clone(key), val: bytes.Clone(val), next: make([]*slNode, lvl)}
	for i := 0; i < lvl; i++ {
		n.next[i] = update[i].next[i]
		update[i].next[i] = n
	}
	s.count++
	return false, 0
}

func (s *skiplist) Get(key []byte) ([]byte, bool) {
	cur := s.head
	for i := s.level - 1; i >= 0; i-- {
		for cur.next[i] != nil && bytes.Compare(cur.next[i].key, key) < 0 {
			cur = cur.next[i]
		}
	}
	if next := cur.next[0]; next != nil && bytes.Equal(next.key, key) {
		return next.val, true
	}
	return nil, false
}

func (s *skiplist) Len() int { return s.count }

// slIterator walks the skiplist in ascending key order.
type slIterator struct {
	cur *slNode
}

func (s *skiplist) Iterator() *slIterator {
	return &slIterator{cur: s.head.next[0]}
}

func (it *slIterator) Valid() bool { return it.cur != nil }
func (it *slIterator) Key() []byte { return it.cur.key }
func (it *slIterator) Val() []byte { return it.cur.val }
func (it *slIterator) Next()       { it.cur = it.cur.next[0] }
