// Package budgetsem implements a strictly FIFO byte-budget semaphore.
//
// A downloader requests capacity according to the byte budget of each
// task. The semaphore guarantees:
//
//   - Strict FIFO: a queued oversized request at the head of the queue is
//     never overtaken by later, smaller requests.
//   - Cancelling the head waiter immediately advances the queue.
//   - When a grant and a cancellation race, exactly one of them wins.
//   - Shrinking the capacity never revokes already-granted quota; the
//     excess stock is absorbed as holders release, and no new grants are
//     made until usage fits within the new capacity.
package budgetsem

import (
	"container/list"
	"context"
	"sync"
)

// Semaphore is a strictly FIFO, resizable byte-budget semaphore.
type Semaphore struct {
	mu       sync.Mutex
	capacity int64
	used     int64     // granted bytes; may exceed capacity after a shrink
	queue    list.List // FIFO queue of *waiter
}

type waiter struct {
	n       int64
	grant   chan struct{}
	granted bool
}

// New returns a Semaphore with the given byte capacity.
func New(capacity int64) *Semaphore {
	return &Semaphore{capacity: capacity}
}

// Acquire blocks until n bytes of budget are granted to the caller or ctx
// is done. If a grant and a cancellation race, exactly one outcome is
// reported: either nil is returned and the caller owns n bytes, or
// ctx.Err() is returned and no budget was consumed.
func (s *Semaphore) Acquire(ctx context.Context, n int64) error {
	if n <= 0 {
		return nil
	}
	s.mu.Lock()
	if s.queue.Len() == 0 && s.used+n <= s.capacity {
		s.used += n
		s.mu.Unlock()
		return nil
	}
	w := &waiter{n: n, grant: make(chan struct{})}
	elem := s.queue.PushBack(w)
	s.mu.Unlock()

	select {
	case <-w.grant:
		return nil
	case <-ctx.Done():
		s.mu.Lock()
		if w.granted {
			// The grant was already committed under the mutex;
			// the grant wins the race. The caller now owns the
			// budget and must release it.
			s.mu.Unlock()
			return nil
		}
		s.queue.Remove(elem)
		// The cancelled waiter may have been the head blocking
		// the queue; advance the queue immediately.
		s.processLocked()
		s.mu.Unlock()
		return ctx.Err()
	}
}

// Release returns n previously granted bytes to the semaphore and grants
// queued waiters in FIFO order.
func (s *Semaphore) Release(n int64) {
	if n <= 0 {
		return
	}
	s.mu.Lock()
	s.used -= n
	if s.used < 0 {
		s.used = 0
	}
	s.processLocked()
	s.mu.Unlock()
}

// SetCapacity resizes the semaphore. Growing may immediately grant queued
// waiters. Shrinking never revokes already-granted quota: usage may stay
// above capacity, and the excess is absorbed as holders release.
func (s *Semaphore) SetCapacity(capacity int64) {
	s.mu.Lock()
	s.capacity = capacity
	s.processLocked()
	s.mu.Unlock()
}

// Used reports the currently granted bytes.
func (s *Semaphore) Used() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.used
}

// Capacity reports the current capacity.
func (s *Semaphore) Capacity() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.capacity
}

// processLocked grants waiters from the head of the queue while they fit.
// It stops at the first waiter that does not fit, so a queued oversized
// request is never overtaken by later smaller ones (strict FIFO).
func (s *Semaphore) processLocked() {
	for s.queue.Len() > 0 {
		elem := s.queue.Front()
		w := elem.Value.(*waiter)
		if s.used+w.n > s.capacity {
			break
		}
		s.used += w.n
		w.granted = true
		close(w.grant)
		s.queue.Remove(elem)
	}
}
