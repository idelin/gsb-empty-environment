// Package swq maintains exact quantiles over a sliding time window.
//
// Values live in an order-statistics skip list (O(log n) insert, delete and
// rank lookup); event expiry is driven by a min-heap on timestamps. No
// batch re-sorting is ever performed: every mutation is incremental.
package swq

import (
	"container/heap"
	"errors"
	"math"
	"math/rand"
	"time"
)

// Event is a single sample inside the sliding window.
type Event struct {
	ID    uint64
	Value float64
	Ts    time.Time
}

// ErrEmpty is returned by Quantile when the window holds no events.
var ErrEmpty = errors.New("swq: window is empty")

// ErrBadQuantile is returned when q is outside [0, 1].
var ErrBadQuantile = errors.New("swq: q must be in [0, 1]")

type expiryItem struct {
	ts time.Time
	id uint64
}

// expiryHeap is a min-heap of events keyed by timestamp.
type expiryHeap []expiryItem

func (h expiryHeap) Len() int            { return len(h) }
func (h expiryHeap) Less(i, j int) bool  { return h[i].ts.Before(h[j].ts) }
func (h expiryHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *expiryHeap) Push(x interface{}) { *h = append(*h, x.(expiryItem)) }
func (h *expiryHeap) Pop() interface{} {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

// Quantiler keeps exact order statistics for the events inside a sliding
// time window of fixed length. The window is half-open: an event with
// timestamp ts is live while ts+window > now, and expires once
// ts+window <= now.
//
// It is not safe for concurrent use.
type Quantiler struct {
	window time.Duration
	sl     *skipList
	live   map[uint64]Event
	expiry expiryHeap
	nextID uint64
	now    time.Time
}

// New creates a Quantiler with the given window size (e.g. 10*time.Second).
func New(window time.Duration) *Quantiler {
	return &Quantiler{
		window: window,
		sl:     newSkipList(rand.New(rand.NewSource(time.Now().UnixNano()))),
		live:   make(map[uint64]Event),
	}
}

// Add inserts one event. Timestamps may arrive out of order; the window
// clock only moves forward, to the newest timestamp seen. Returns the
// event ID, usable with Remove.
func (q *Quantiler) Add(value float64, ts time.Time) uint64 {
	if ts.After(q.now) {
		q.now = ts
	}
	q.expire()

	id := q.nextID
	q.nextID++
	q.live[id] = Event{ID: id, Value: value, Ts: ts}
	q.sl.insert(value, id)
	heap.Push(&q.expiry, expiryItem{ts: ts, id: id})
	// A late arrival may already be outside the window; expire eagerly so
	// the window never holds stale events.
	q.expire()
	return id
}

// Advance moves the window clock to now (never backwards) and expires
// events that fell out of the window.
func (q *Quantiler) Advance(now time.Time) {
	if now.After(q.now) {
		q.now = now
	}
	q.expire()
}

// Remove retracts a single live event by ID. Returns false if the ID is
// unknown or already expired/removed.
func (q *Quantiler) Remove(id uint64) bool {
	ev, ok := q.live[id]
	if !ok {
		return false
	}
	delete(q.live, id)
	return q.sl.delete(ev.Value, ev.ID)
}

// Len returns the number of live events in the window.
func (q *Quantiler) Len() int { return q.sl.length }

// Quantile returns the exact q-quantile of the live values, located
// strictly at rank floor(q*(n-1)) of the sorted values.
func (q *Quantiler) Quantile(quantile float64) (float64, error) {
	if quantile < 0 || quantile > 1 {
		return 0, ErrBadQuantile
	}
	n := q.sl.length
	if n == 0 {
		return 0, ErrEmpty
	}
	rank := int(math.Floor(quantile * float64(n-1)))
	v, _, _ := q.sl.kth(rank)
	return v, nil
}

// expire drops every event whose retention elapsed, in timestamp order.
// Heap entries belonging to removed events are skipped lazily.
func (q *Quantiler) expire() {
	for len(q.expiry) > 0 {
		top := q.expiry[0]
		if top.ts.Add(q.window).After(q.now) {
			break
		}
		heap.Pop(&q.expiry)
		ev, ok := q.live[top.id]
		if !ok || !ev.Ts.Equal(top.ts) {
			continue // retracted already
		}
		delete(q.live, top.id)
		q.sl.delete(ev.Value, ev.ID)
	}
}
