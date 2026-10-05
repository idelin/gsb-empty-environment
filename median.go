// Package median maintains a multiset of numeric values keyed by ID and
// answers lower-median and absolute-deviation-sum queries.
//
// The structure uses two heaps (a max-heap for the lower half and a
// min-heap for the upper half) with lazy deletion: removals only mark a
// tombstone, and the physical heaps are rebuilt once the number of
// physical nodes exceeds 2*active + 32.
package median

import (
	"container/heap"
	"errors"
)

// ErrNotFound is returned when removing an ID that does not exist.
var ErrNotFound = errors.New("median: id not found")

// ErrDuplicate is returned when adding an ID that already exists.
var ErrDuplicate = errors.New("median: id already exists")

// slack is the number of extra physical nodes tolerated before a rebuild.
const slack = 32

type item struct {
	value int64
	id    int
	seq   int64 // insertion sequence, distinguishes nodes of a reused id
}

// maxHeap stores the lower half; the largest element is on top.
type maxHeap []item

func (h maxHeap) Len() int { return len(h) }
func (h maxHeap) Less(i, j int) bool {
	if h[i].value != h[j].value {
		return h[i].value > h[j].value
	}
	return h[i].seq > h[j].seq
}
func (h maxHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *maxHeap) Push(x interface{}) { *h = append(*h, x.(item)) }
func (h *maxHeap) Pop() interface{} {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

// minHeap stores the upper half; the smallest element is on top.
type minHeap []item

func (h minHeap) Len() int { return len(h) }
func (h minHeap) Less(i, j int) bool {
	if h[i].value != h[j].value {
		return h[i].value < h[j].value
	}
	return h[i].seq < h[j].seq
}
func (h minHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *minHeap) Push(x interface{}) { *h = append(*h, x.(item)) }
func (h *minHeap) Pop() interface{} {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

// entry describes an active value.
type entry struct {
	value int64
	seq   int64
	high  bool // true when the node lives in the upper-half heap
}

// Tracker is a dynamic multiset supporting add/remove by ID and
// median/deviation queries. It is not safe for concurrent use.
type Tracker struct {
	low  maxHeap // physical nodes of the lower half (may hold tombstones)
	high minHeap // physical nodes of the upper half (may hold tombstones)

	lowSize  int // logical (active) size of the lower half
	highSize int // logical (active) size of the upper half
	sumLow   int64
	sumHigh  int64

	entries map[int]entry // id -> active entry
	tombs   map[int]int64 // id -> seq of the removed node (lazy deletion)
	nextSeq int64
}

// New returns an empty Tracker.
func New() *Tracker {
	return &Tracker{
		entries: make(map[int]entry),
		tombs:   make(map[int]int64),
	}
}

// Len reports the number of active entries.
func (t *Tracker) Len() int { return len(t.entries) }

// physical reports the number of nodes stored in both heaps, including
// tombstoned nodes awaiting cleanup.
func (t *Tracker) physical() int { return len(t.low) + len(t.high) }

// dead reports whether the physical node is a tombstoned leftover.
func (t *Tracker) dead(it item) bool {
	seq, ok := t.tombs[it.id]
	return ok && seq == it.seq
}

// Add inserts value under id. Equal values are distinguished by id.
// It returns ErrDuplicate if id is already present.
func (t *Tracker) Add(id int, value int64) error {
	if _, ok := t.entries[id]; ok {
		return ErrDuplicate
	}
	t.nextSeq++
	seq := t.nextSeq
	it := item{value: value, id: id, seq: seq}
	if t.lowSize > 0 && value > t.lowTop() {
		heap.Push(&t.high, it)
		t.highSize++
		t.sumHigh += value
		t.entries[id] = entry{value, seq, true}
	} else {
		heap.Push(&t.low, it)
		t.lowSize++
		t.sumLow += value
		t.entries[id] = entry{value, seq, false}
	}
	t.rebalance()
	t.maybeRebuild()
	return nil
}

// Remove deletes the entry with the given id. The node is only marked as
// removed and is physically discarded lazily. It returns ErrNotFound if
// id does not exist.
func (t *Tracker) Remove(id int) error {
	e, ok := t.entries[id]
	if !ok {
		return ErrNotFound
	}
	delete(t.entries, id)
	t.tombs[id] = e.seq
	if e.high {
		t.highSize--
		t.sumHigh -= e.value
	} else {
		t.lowSize--
		t.sumLow -= e.value
	}
	t.rebalance()
	t.maybeRebuild()
	return nil
}

// Query returns the lower median and the absolute-deviation sums of the
// two sides: left is sum(median - x) over the lower half (including the
// median itself), right is sum(x - median) over the upper half. ok is
// false when the structure is empty.
func (t *Tracker) Query() (median, left, right int64, ok bool) {
	if len(t.entries) == 0 {
		return 0, 0, 0, false
	}
	median = t.lowTop()
	left = median*int64(t.lowSize) - t.sumLow
	right = t.sumHigh - median*int64(t.highSize)
	return median, left, right, true
}

// lowTop returns the top of the lower heap, discarding tombstoned nodes.
// Callers must ensure lowSize > 0.
func (t *Tracker) lowTop() int64 {
	t.pruneLow()
	return t.low[0].value
}

func (t *Tracker) pruneLow() {
	for len(t.low) > 0 && t.dead(t.low[0]) {
		delete(t.tombs, t.low[0].id)
		heap.Pop(&t.low)
	}
}

func (t *Tracker) pruneHigh() {
	for len(t.high) > 0 && t.dead(t.high[0]) {
		delete(t.tombs, t.high[0].id)
		heap.Pop(&t.high)
	}
}

// rebalance restores lowSize >= highSize and lowSize-highSize <= 1 by
// moving active boundary elements between the heaps.
func (t *Tracker) rebalance() {
	for t.lowSize > t.highSize+1 {
		t.pruneLow()
		it := heap.Pop(&t.low).(item)
		heap.Push(&t.high, it)
		t.lowSize--
		t.highSize++
		t.sumLow -= it.value
		t.sumHigh += it.value
		t.entries[it.id] = entry{it.value, it.seq, true}
	}
	for t.lowSize < t.highSize {
		t.pruneHigh()
		it := heap.Pop(&t.high).(item)
		heap.Push(&t.low, it)
		t.lowSize++
		t.highSize--
		t.sumLow += it.value
		t.sumHigh -= it.value
		t.entries[it.id] = entry{it.value, it.seq, false}
	}
}

// maybeRebuild physically discards tombstoned nodes once the heaps hold
// more than 2*active + slack nodes.
func (t *Tracker) maybeRebuild() {
	if t.physical() > 2*len(t.entries)+slack {
		t.rebuild()
	}
}

// rebuild drops every tombstoned node and re-heapifies both sides.
func (t *Tracker) rebuild() {
	low := make(maxHeap, 0, t.lowSize)
	for _, it := range t.low {
		if !t.dead(it) {
			low = append(low, it)
		}
	}
	high := make(minHeap, 0, t.highSize)
	for _, it := range t.high {
		if !t.dead(it) {
			high = append(high, it)
		}
	}
	heap.Init(&low)
	heap.Init(&high)
	t.low = low
	t.high = high
	t.tombs = make(map[int]int64)
}
