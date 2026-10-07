// Package evsched implements an event scheduler whose priority queue can be
// forked into independent versions (branches) and merged back together.
//
// Events are ordered by time and then by id. Re-scheduling an event (Upsert
// on an existing id) bumps that event's per-id revision number and lazily
// invalidates the old heap entry. Revision numbers are monotonic per branch:
// Cancel removes the live entry but does not rewind the id's revision
// counter, so a later re-add still counts as a newer revision. Cancel only
// affects the current branch. Merging two branches keeps, for each id, the
// entry with the larger revision number; equal revision numbers with
// different content is reported as a ConflictError.
package evsched

import (
	"container/heap"
	"errors"
	"fmt"
	"sort"
)

// Event is a scheduled item in the queue.
type Event struct {
	ID   string
	Time int64
	Data string
}

// entry is the live record for an id in one branch.
type entry struct {
	rev  uint64
	time int64
	data string
}

// heapItem is one (possibly stale) occurrence of an event in the heap.
type heapItem struct {
	time int64
	id   string
	rev  uint64
}

// itemHeap is a min-heap ordered by (time, id).
type itemHeap []heapItem

func (h itemHeap) Len() int { return len(h) }

func (h itemHeap) Less(i, j int) bool {
	if h[i].time != h[j].time {
		return h[i].time < h[j].time
	}
	return h[i].id < h[j].id
}

func (h itemHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *itemHeap) Push(x interface{}) { *h = append(*h, x.(heapItem)) }

func (h *itemHeap) Pop() interface{} {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

// ConflictError is returned by Merge when both branches contain the same id
// at the same revision number but with different content.
type ConflictError struct {
	ID  string
	Rev uint64
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("evsched: merge conflict on id %q: revision %d has different content in the two branches", e.ID, e.Rev)
}

// ErrDifferentSchedulers is returned by Merge when the two queues do not
// belong to the same Scheduler.
var ErrDifferentSchedulers = errors.New("evsched: cannot merge queues from different schedulers")

// Scheduler creates new queue versions. Queues derived from different
// Schedulers cannot be merged.
type Scheduler struct {
	marker int // non-zero-size so distinct Schedulers have distinct pointers
}

// NewScheduler returns a Scheduler from which initial queue versions can be
// created.
func NewScheduler() *Scheduler { return &Scheduler{} }

// Queue is a handle to one version (branch) of the event queue. Mutating a
// Queue never affects any other Queue.
type Queue struct {
	s       *Scheduler
	entries map[string]entry
	revs    map[string]uint64 // last revision issued per id, survives Cancel
	pq      itemHeap          // lazy-deleted min-heap by (time, id)
}

// NewQueue returns an empty queue version.
func (s *Scheduler) NewQueue() *Queue {
	return &Queue{s: s, entries: make(map[string]entry), revs: make(map[string]uint64)}
}

// Fork returns an independent copy of this version. Subsequent changes to
// either version do not affect the other.
func (q *Queue) Fork() *Queue {
	entries := make(map[string]entry, len(q.entries))
	for id, e := range q.entries {
		entries[id] = e
	}
	revs := make(map[string]uint64, len(q.revs))
	for id, r := range q.revs {
		revs[id] = r
	}
	pq := make(itemHeap, len(q.pq))
	copy(pq, q.pq)
	return &Queue{s: q.s, entries: entries, revs: revs, pq: pq}
}

// Upsert schedules a new event or reschedules an existing one. The id's
// revision number is incremented and any previous heap entry for the id
// becomes stale.
func (q *Queue) Upsert(id string, time int64, data string) {
	rev := q.revs[id] + 1
	q.revs[id] = rev
	q.entries[id] = entry{rev: rev, time: time, data: data}
	heap.Push(&q.pq, heapItem{time: time, id: id, rev: rev})
}

// Cancel removes the event with the given id from this branch only. The id's
// revision counter is retained, so a later Upsert still counts as newer than
// any revision visible before the Cancel. It reports whether the event was
// present.
func (q *Queue) Cancel(id string) bool {
	if _, ok := q.entries[id]; !ok {
		return false
	}
	delete(q.entries, id)
	return true
}

// live reports whether the heap item (id, rev) is still the current entry.
func (q *Queue) live(id string, rev uint64) bool {
	e, ok := q.entries[id]
	return ok && e.rev == rev
}

// Peek returns the minimum event by (time, id) without removing it, lazily
// discarding stale heap entries.
func (q *Queue) Peek() (Event, bool) {
	for len(q.pq) > 0 {
		top := q.pq[0]
		if q.live(top.id, top.rev) {
			return Event{ID: top.id, Time: top.time, Data: q.entries[top.id].data}, true
		}
		heap.Pop(&q.pq)
	}
	return Event{}, false
}

// Pop removes and returns the minimum event by (time, id).
func (q *Queue) Pop() (Event, bool) {
	ev, ok := q.Peek()
	if !ok {
		return Event{}, false
	}
	delete(q.entries, ev.ID)
	heap.Pop(&q.pq)
	return ev, true
}

// Len returns the number of live events in this version.
func (q *Queue) Len() int { return len(q.entries) }

// Contains reports whether the id has a live event in this version.
func (q *Queue) Contains(id string) bool {
	_, ok := q.entries[id]
	return ok
}

// Snapshot returns all live events of this version sorted by (time, id).
func (q *Queue) Snapshot() []Event {
	out := make([]Event, 0, len(q.entries))
	for id, e := range q.entries {
		out = append(out, Event{ID: id, Time: e.time, Data: e.data})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Time != out[j].Time {
			return out[i].Time < out[j].Time
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Merge combines two branches into a new version. For each id present in
// both branches the entry with the larger revision number wins; equal
// revision numbers with different content yields a *ConflictError. Ids
// present in only one branch are carried over. The inputs are not modified.
func Merge(a, b *Queue) (*Queue, error) {
	if a.s != b.s {
		return nil, ErrDifferentSchedulers
	}
	merged := make(map[string]entry, len(a.entries)+len(b.entries))
	for id, e := range a.entries {
		merged[id] = e
	}
	for id, e := range b.entries {
		cur, ok := merged[id]
		switch {
		case !ok || e.rev > cur.rev:
			merged[id] = e
		case e.rev == cur.rev:
			if e.time != cur.time || e.data != cur.data {
				return nil, &ConflictError{ID: id, Rev: e.rev}
			}
		}
	}
	revs := make(map[string]uint64, len(a.revs)+len(b.revs))
	for id, r := range a.revs {
		revs[id] = r
	}
	for id, r := range b.revs {
		if r > revs[id] {
			revs[id] = r
		}
	}
	pq := make(itemHeap, 0, len(merged))
	for id, e := range merged {
		pq = append(pq, heapItem{time: e.time, id: id, rev: e.rev})
	}
	heap.Init(&pq)
	return &Queue{s: a.s, entries: merged, revs: revs, pq: pq}, nil
}
