// Package slidewin implements a sliding-time-window exact quantile
// maintainer. Values live in an order-statistics skip list (each level
// carries a span, Redis-zskiplist style), so the k-th smallest element is
// located in O(log n) without ever re-sorting the window. Expiration is
// driven by a min-heap on event timestamps, and every event can be
// retracted individually by id.
package slidewin

import (
	"container/heap"
	"math"
	"math/rand"
	"time"
)

const (
	slMaxLevel = 32
	slBranch   = 0.25
)

// ---------------------------------------------------------------------------
// order-statistics skip list, keyed by (value, id) so duplicates coexist
// ---------------------------------------------------------------------------

type slLevel struct {
	forward *slNode
	span    int // nodes between this node and forward at this level
}

type slNode struct {
	value  float64
	id     uint64
	levels []slLevel
}

type skipList struct {
	head   *slNode
	level  int
	length int
	rng    *rand.Rand
}

func newSkipList() *skipList {
	return &skipList{
		head:  &slNode{levels: make([]slLevel, slMaxLevel)},
		level: 1,
		rng:   rand.New(rand.NewSource(0x5eed)),
	}
}

func lessKey(av float64, ai uint64, bv float64, bi uint64) bool {
	return av < bv || (av == bv && ai < bi)
}

func (sl *skipList) randomLevel() int {
	lvl := 1
	for lvl < slMaxLevel && sl.rng.Float64() < slBranch {
		lvl++
	}
	return lvl
}

func (sl *skipList) insert(value float64, id uint64) {
	var update [slMaxLevel]*slNode
	var rank [slMaxLevel]int

	x := sl.head
	for i := sl.level - 1; i >= 0; i-- {
		if i == sl.level-1 {
			rank[i] = 0
		} else {
			rank[i] = rank[i+1]
		}
		for x.levels[i].forward != nil &&
			lessKey(x.levels[i].forward.value, x.levels[i].forward.id, value, id) {
			rank[i] += x.levels[i].span
			x = x.levels[i].forward
		}
		update[i] = x
	}

	lvl := sl.randomLevel()
	if lvl > sl.level {
		for i := sl.level; i < lvl; i++ {
			rank[i] = 0
			update[i] = sl.head
			update[i].levels[i].span = sl.length
		}
		sl.level = lvl
	}

	x = &slNode{value: value, id: id, levels: make([]slLevel, lvl)}
	for i := 0; i < lvl; i++ {
		x.levels[i].forward = update[i].levels[i].forward
		update[i].levels[i].forward = x
		x.levels[i].span = update[i].levels[i].span - (rank[0] - rank[i])
		update[i].levels[i].span = (rank[0] - rank[i]) + 1
	}
	for i := lvl; i < sl.level; i++ {
		update[i].levels[i].span++
	}
	sl.length++
}

func (sl *skipList) delete(value float64, id uint64) bool {
	var update [slMaxLevel]*slNode

	x := sl.head
	for i := sl.level - 1; i >= 0; i-- {
		for x.levels[i].forward != nil &&
			lessKey(x.levels[i].forward.value, x.levels[i].forward.id, value, id) {
			x = x.levels[i].forward
		}
		update[i] = x
	}
	x = x.levels[0].forward
	if x == nil || x.value != value || x.id != id {
		return false
	}
	for i := 0; i < sl.level; i++ {
		if update[i].levels[i].forward == x {
			update[i].levels[i].span += x.levels[i].span - 1
			update[i].levels[i].forward = x.levels[i].forward
		} else {
			update[i].levels[i].span--
		}
	}
	for sl.level > 1 && sl.head.levels[sl.level-1].forward == nil {
		sl.level--
	}
	sl.length--
	return true
}

// atRank returns the node at 0-based rank k (k-th smallest).
func (sl *skipList) atRank(k int) *slNode {
	target := k + 1 // spans count 1-indexed steps
	traversed := 0
	x := sl.head
	for i := sl.level - 1; i >= 0; i-- {
		for x.levels[i].forward != nil && traversed+x.levels[i].span <= target {
			traversed += x.levels[i].span
			x = x.levels[i].forward
		}
		if traversed == target {
			return x
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// sliding window
// ---------------------------------------------------------------------------

type event struct {
	ts    time.Time
	value float64
	id    uint64
}

// expiryHeap is a min-heap ordered by (ts, id).
type expiryHeap []event

func (h expiryHeap) Len() int { return len(h) }
func (h expiryHeap) Less(i, j int) bool {
	if h[i].ts.Equal(h[j].ts) {
		return h[i].id < h[j].id
	}
	return h[i].ts.Before(h[j].ts)
}
func (h expiryHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *expiryHeap) Push(x interface{}) { *h = append(*h, x.(event)) }
func (h *expiryHeap) Pop() interface{} {
	old := *h
	n := len(old)
	e := old[n-1]
	*h = old[:n-1]
	return e
}

// Window maintains the exact multiset of values whose timestamps fall in
// the half-open interval (now-size, now]. An event expires as soon as
// now-ts >= size. All operations are O(log n).
type Window struct {
	size    time.Duration
	now     time.Time
	started bool
	sl      *skipList
	exp     expiryHeap
	live    map[uint64]event
	nextID  uint64
}

// NewWindow creates a window covering the most recent `size` of time.
func NewWindow(size time.Duration) *Window {
	return &Window{
		size: size,
		sl:   newSkipList(),
		live: make(map[uint64]event),
	}
}

// expire drops every event with ts <= now-size.
func (w *Window) expire() {
	cutoff := w.now.Add(-w.size)
	for len(w.exp) > 0 && !w.exp[0].ts.After(cutoff) {
		ev := heap.Pop(&w.exp).(event)
		if _, ok := w.live[ev.id]; !ok {
			continue // retracted earlier; lazy heap deletion
		}
		delete(w.live, ev.id)
		w.sl.delete(ev.value, ev.id)
	}
}

// Add inserts an event. Out-of-order timestamps are accepted; an event
// that is already expired relative to the current window head is dropped
// and reported as not live. Returns the event id (for Retract) and
// whether the event actually entered the window.
func (w *Window) Add(ts time.Time, value float64) (uint64, bool) {
	if !w.started || ts.After(w.now) {
		w.now = ts
		w.started = true
	}
	w.expire()

	id := w.nextID
	w.nextID++
	if !ts.After(w.now.Add(-w.size)) {
		return id, false // born expired
	}
	ev := event{ts: ts, value: value, id: id}
	w.live[id] = ev
	heap.Push(&w.exp, ev)
	w.sl.insert(value, id)
	return id, true
}

// Advance moves the window head to ts (never backwards), expiring events.
func (w *Window) Advance(ts time.Time) {
	if !w.started || ts.After(w.now) {
		w.now = ts
		w.started = true
	}
	w.expire()
}

// Retract removes a single live event by id. Duplicate values are removed
// one occurrence at a time. Returns false if the id is unknown or already
// gone.
func (w *Window) Retract(id uint64) bool {
	ev, ok := w.live[id]
	if !ok {
		return false
	}
	delete(w.live, id)
	return w.sl.delete(ev.value, ev.id)
}

// Len returns the number of live events.
func (w *Window) Len() int { return w.sl.length }

// Quantile returns the value at rank floor(q*(n-1)) of the live multiset,
// i.e. the exact k-th smallest element. ok is false when the window is
// empty. q is clamped to [0, 1].
func (w *Window) Quantile(q float64) (value float64, ok bool) {
	n := w.sl.length
	if n == 0 {
		return 0, false
	}
	if q < 0 {
		q = 0
	}
	if q > 1 {
		q = 1
	}
	k := int(math.Floor(q * float64(n-1)))
	node := w.sl.atRank(k)
	if node == nil {
		return 0, false
	}
	return node.value, true
}
