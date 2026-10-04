// Package batchq implements a capacity-bounded batch task queue.
//
// Callers submit a batch of tasks and choose an admission mode:
// either the whole batch is admitted (AdmitAll), or only the longest
// prefix that fits in the remaining capacity is admitted (AdmitPrefix).
// Admission reserves capacity and enqueues the admitted tasks
// atomically, so an admitted task is always both reserved and queued.
//
// Batches are served strictly in arrival order: a task from a later
// batch is never admitted before a task from an earlier batch.
//
// A successful admission returns Reservations. Cancelling a
// Reservation after it was granted follows one uniform rule: the
// reserved capacity is returned to the queue and the task is removed
// from the queue, exactly once, no matter when the cancel happens.
package batchq

import (
	"errors"
	"sync"
	"sync/atomic"
)

// Mode selects how a batch is admitted.
type Mode int

const (
	// AdmitAll admits the whole batch if it fits, otherwise nothing.
	AdmitAll Mode = iota
	// AdmitPrefix admits the longest prefix of the batch that fits in
	// the remaining capacity; the rest is rejected.
	AdmitPrefix
)

// ErrInvalidTask is returned when a batch contains a task with a
// non-positive size or an empty ID.
var ErrInvalidTask = errors.New("batchq: task must have a non-empty ID and a positive size")

// Task is a unit of work requesting Size units of capacity.
type Task struct {
	ID   string
	Size int64
}

// EventKind identifies the kind of a recorded history event.
type EventKind int

const (
	// EventAdmit records one serialized admission decision.
	EventAdmit EventKind = iota
	// EventCancel records one capacity refund.
	EventCancel
)

// Event is one entry of the queue's serialized history. It allows
// callers and tests to replay every capacity change in the exact
// order it happened.
type Event struct {
	Kind EventKind
	// Seq is the arrival sequence number of the batch (admit events only).
	Seq uint64
	// Mode is the admission mode of the batch (admit events only).
	Mode Mode
	// Requested lists the task IDs of the batch, in order (admit events only).
	Requested []string
	// Admitted lists the admitted task IDs, always a prefix of Requested
	// (admit events only).
	Admitted []string
	// TaskID is the cancelled task (cancel events only).
	TaskID string
	// Used is the reserved capacity right after the event.
	Used int64
}

// Reservation is a handle to one admitted task. Its capacity stays
// reserved until Cancel is called.
type Reservation struct {
	task Task
	q    *Queue
	once sync.Once
}

// Task returns the admitted task.
func (r *Reservation) Task() Task { return r.task }

// Cancel releases the reservation: the task is removed from the queue
// and its capacity is returned. The rule is uniform regardless of when
// Cancel is called, and calling it more than once has no extra effect.
func (r *Reservation) Cancel() {
	r.once.Do(func() {
		r.q.release(r.task)
	})
}

// Queue is a capacity-bounded batch task queue. It is safe for
// concurrent use.
type Queue struct {
	mu   sync.Mutex
	cond *sync.Cond

	capacity int64
	used     int64

	// nextSeq hands out arrival tickets; serving is the ticket
	// currently allowed to reserve. Serving batches in ticket order
	// guarantees that no later task is admitted before an earlier one.
	nextSeq uint64
	serving uint64

	enqueued []Task
	events   []Event
}

// New returns a Queue that admits at most capacity units of tasks.
func New(capacity int64) *Queue {
	if capacity < 0 {
		capacity = 0
	}
	q := &Queue{capacity: capacity}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// Capacity returns the total capacity of the queue.
func (q *Queue) Capacity() int64 { return q.capacity }

// Used returns the currently reserved capacity.
func (q *Queue) Used() int64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.used
}

// Snapshot returns the tasks currently in the queue, in enqueue order.
func (q *Queue) Snapshot() []Task {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]Task, len(q.enqueued))
	copy(out, q.enqueued)
	return out
}

// Events returns the serialized admission/cancellation history.
func (q *Queue) Events() []Event {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]Event, len(q.events))
	copy(out, q.events)
	return out
}

// Admit tries to reserve capacity for batch and enqueue its tasks
// according to mode. It returns a Reservation per admitted task and
// the rejected remainder of the batch. Batches are served in arrival
// order, so a caller may block until every earlier batch has been
// served; admission itself never waits for capacity.
func (q *Queue) Admit(batch []Task, mode Mode) (admitted []*Reservation, rejected []Task, err error) {
	for _, t := range batch {
		if t.ID == "" || t.Size <= 0 {
			return nil, nil, ErrInvalidTask
		}
	}

	seq := atomic.AddUint64(&q.nextSeq, 1) - 1

	q.mu.Lock()
	for q.serving != seq {
		q.cond.Wait()
	}

	n := 0
	switch mode {
	case AdmitAll:
		var total int64
		for _, t := range batch {
			total += t.Size
		}
		if q.used+total <= q.capacity {
			n = len(batch)
		}
	case AdmitPrefix:
		used := q.used
		for n < len(batch) && used+batch[n].Size <= q.capacity {
			used += batch[n].Size
			n++
		}
	}

	admitted = make([]*Reservation, 0, n)
	ev := Event{
		Kind:      EventAdmit,
		Seq:       seq,
		Mode:      mode,
		Requested: make([]string, 0, len(batch)),
		Admitted:  make([]string, 0, n),
	}
	for _, t := range batch {
		ev.Requested = append(ev.Requested, t.ID)
	}
	for _, t := range batch[:n] {
		q.used += t.Size
		q.enqueued = append(q.enqueued, t)
		admitted = append(admitted, &Reservation{task: t, q: q})
		ev.Admitted = append(ev.Admitted, t.ID)
	}
	if n < len(batch) {
		rejected = make([]Task, len(batch)-n)
		copy(rejected, batch[n:])
	}
	ev.Used = q.used
	q.events = append(q.events, ev)

	q.serving++
	q.cond.Broadcast()
	q.mu.Unlock()
	return admitted, rejected, nil
}

// release is the single, uniform refund path used by Cancel.
func (q *Queue) release(t Task) {
	q.mu.Lock()
	q.used -= t.Size
	for i, e := range q.enqueued {
		if e.ID == t.ID {
			q.enqueued = append(q.enqueued[:i], q.enqueued[i+1:]...)
			break
		}
	}
	q.events = append(q.events, Event{
		Kind:   EventCancel,
		TaskID: t.ID,
		Used:   q.used,
	})
	q.mu.Unlock()
}
