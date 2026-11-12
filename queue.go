package netsec

import (
	"errors"
	"sync"
)

var (
	// ErrQueueStopped is returned when dispatching to a stopped queue.
	ErrQueueStopped = errors.New("netsec: queue stopped")
	// ErrQueueEmpty is returned when completing from a drained queue.
	ErrQueueEmpty = errors.New("netsec: queue empty")
)

// Queue simulates one device DMA queue. It accepts submitted operations,
// completes them in order (allowing partial completion), and mints stop
// proofs required for device reset.
type Queue struct {
	mu      sync.Mutex
	name    string
	tracker *Tracker
	pending []OpID
	stopped bool
	epoch   uint64
}

// NewQueue creates a queue attached to the tracker.
func NewQueue(name string, t *Tracker) *Queue {
	q := &Queue{name: name, tracker: t}
	t.AttachQueue(q)
	return q
}

// Dispatch enqueues an operation previously accepted by the tracker.
func (q *Queue) Dispatch(op OpID) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.stopped {
		return ErrQueueStopped
	}
	q.pending = append(q.pending, op)
	return nil
}

// CompleteOne retires the oldest pending operation, confirming it with the
// tracker. Leaving other operations pending models partial completion.
func (q *Queue) CompleteOne() (OpID, error) {
	q.mu.Lock()
	if len(q.pending) == 0 {
		q.mu.Unlock()
		return 0, ErrQueueEmpty
	}
	op := q.pending[0]
	q.pending = q.pending[1:]
	q.mu.Unlock()
	if err := q.tracker.Complete(op); err != nil {
		return 0, err
	}
	return op, nil
}

// Pending reports how many operations remain in the queue.
func (q *Queue) Pending() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.pending)
}

// Stop halts the queue and returns a stop proof. The proof stays valid only
// while the queue remains stopped and drained; restarting the queue or
// leaving operations pending invalidates it.
func (q *Queue) Stop() StopProof {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.stopped = true
	q.epoch++
	return StopProof{queue: q, epoch: q.epoch}
}

// Start resumes a stopped queue, invalidating previously issued proofs.
func (q *Queue) Start() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.stopped = false
	q.epoch++
}

// StopProof is evidence, minted only by Queue.Stop, that a queue was stopped
// at a particular epoch. It is validated by Tracker.Reset.
type StopProof struct {
	queue *Queue
	epoch uint64
}

func (p StopProof) valid() bool {
	if p.queue == nil {
		return false
	}
	p.queue.mu.Lock()
	defer p.queue.mu.Unlock()
	return p.queue.stopped && p.queue.epoch == p.epoch && len(p.queue.pending) == 0
}
