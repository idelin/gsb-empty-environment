// Package bqueue implements a broadcast queue: each published payload is
// shared by every subscriber that joined before the publish, and a slot is
// reclaimed only after all of its expected receivers have acknowledged it
// by receiving the message (or by leaving, which acknowledges their unread
// messages on their behalf).
package bqueue

import (
	"context"
	"errors"
	"sync"
)

var (
	// ErrClosed is returned by operations on a closed queue.
	ErrClosed = errors.New("bqueue: queue closed")
	// ErrSubClosed is returned by Receive on a closed subscription.
	ErrSubClosed = errors.New("bqueue: subscription closed")
)

// Queue is a fixed-capacity broadcast queue. The zero value is not usable;
// create one with New.
type Queue struct {
	mu      sync.Mutex
	changed chan struct{} // closed and replaced to wake all waiters

	slots   []slot
	head    int // ring index of the oldest committed slot
	count   int // number of committed slots
	nextSeq uint64

	subs   map[*Subscription]struct{}
	closed bool
}

// slot holds one committed message and its receive-set: the subscribers
// that are still expected to receive (acknowledge) it before the slot may
// be reclaimed.
type slot struct {
	seq     uint64
	value   interface{}
	pending map[*Subscription]struct{}
}

// New returns a Queue that can hold at most capacity unacknowledged
// messages.
func New(capacity int) *Queue {
	if capacity <= 0 {
		panic("bqueue: capacity must be positive")
	}
	return &Queue{
		changed: make(chan struct{}),
		slots:   make([]slot, capacity),
		subs:    make(map[*Subscription]struct{}),
	}
}

// Subscribe returns a new Subscription. The subscriber only receives
// messages published after this call; it never sees older messages.
// Subscribe on a closed queue returns nil.
func (q *Queue) Subscribe() *Subscription {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return nil
	}
	s := &Subscription{q: q, next: q.nextSeq}
	q.subs[s] = struct{}{}
	return s
}

// Publish commits v as the next message and returns its sequence number.
// Every current subscriber becomes an expected receiver of the message.
//
// If the queue is full, Publish blocks until a slot is reclaimed, the
// queue is closed, or ctx is cancelled. A cancellation only abandons the
// publish if it happens before the commit; once committed, the sequence
// number is always returned, even if ctx is already cancelled.
func (q *Queue) Publish(ctx context.Context, v interface{}) (uint64, error) {
	q.mu.Lock()
	for !q.closed && q.count == len(q.slots) && ctx.Err() == nil {
		q.waitLocked(ctx)
	}
	if q.closed {
		q.mu.Unlock()
		return 0, ErrClosed
	}
	if q.count == len(q.slots) {
		// Cancelled while waiting for capacity: nothing was committed.
		err := ctx.Err()
		q.mu.Unlock()
		return 0, err
	}
	seq := q.nextSeq
	q.nextSeq++
	pending := make(map[*Subscription]struct{}, len(q.subs))
	for s := range q.subs {
		pending[s] = struct{}{}
	}
	idx := (q.head + q.count) % len(q.slots)
	q.slots[idx] = slot{seq: seq, value: v, pending: pending}
	q.count++
	q.broadcastLocked()
	q.mu.Unlock()
	return seq, nil
}

// Close closes the queue. Blocked and future Publish calls fail with
// ErrClosed; subscriptions may still drain already committed messages.
func (q *Queue) Close() {
	q.mu.Lock()
	if !q.closed {
		q.closed = true
		q.broadcastLocked()
	}
	q.mu.Unlock()
}

// Len reports the number of committed, not yet reclaimed messages.
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.count
}

// waitLocked blocks until the queue state changes or ctx is done.
// q.mu must be held; it is held again on return.
func (q *Queue) waitLocked(ctx context.Context) {
	ch := q.changed
	q.mu.Unlock()
	select {
	case <-ch:
	case <-ctx.Done():
	}
	q.mu.Lock()
}

func (q *Queue) broadcastLocked() {
	close(q.changed)
	q.changed = make(chan struct{})
}

// releaseLocked reclaims fully acknowledged slots from the head and wakes
// publishers waiting for capacity.
func (q *Queue) releaseLocked() {
	released := false
	for q.count > 0 && len(q.slots[q.head].pending) == 0 {
		q.slots[q.head] = slot{}
		q.head = (q.head + 1) % len(q.slots)
		q.count--
		released = true
	}
	if released {
		q.broadcastLocked()
	}
}

// Subscription is a receiver's handle on a Queue. It is not safe for
// concurrent use by multiple goroutines.
type Subscription struct {
	q      *Queue
	next   uint64 // sequence number of the next message to receive
	closed bool
}

// Receive returns the next message for this subscriber, acknowledging it.
// It blocks until a message is available, the subscription or queue is
// closed, or ctx is cancelled. A message that became available before the
// cancellation is still delivered.
func (s *Subscription) Receive(ctx context.Context) (uint64, interface{}, error) {
	q := s.q
	q.mu.Lock()
	for !s.closed && !q.closed && s.next >= q.nextSeq && ctx.Err() == nil {
		q.waitLocked(ctx)
	}
	if s.closed {
		q.mu.Unlock()
		return 0, nil, ErrSubClosed
	}
	if s.next < q.nextSeq {
		headSeq := q.slots[q.head].seq
		idx := (q.head + int(s.next-headSeq)) % len(q.slots)
		sl := &q.slots[idx]
		seq, v := sl.seq, sl.value
		delete(sl.pending, s)
		s.next++
		q.releaseLocked()
		q.mu.Unlock()
		return seq, v, nil
	}
	if q.closed {
		q.mu.Unlock()
		return 0, nil, ErrClosed
	}
	err := ctx.Err()
	q.mu.Unlock()
	return 0, nil, err
}

// Close removes the subscription. Leaving acknowledges all of its unread
// messages, so the corresponding slots can be reclaimed once every other
// expected receiver has also acknowledged them. Close is idempotent.
func (s *Subscription) Close() {
	q := s.q
	q.mu.Lock()
	if s.closed {
		q.mu.Unlock()
		return
	}
	s.closed = true
	delete(q.subs, s)
	for i := 0; i < q.count; i++ {
		idx := (q.head + i) % len(q.slots)
		delete(q.slots[idx].pending, s)
	}
	q.releaseLocked()
	q.mu.Unlock()
}
