// Package budgetsem implements a byte-budget semaphore for downloaders.
//
// A Semaphore hands out byte quotas to tasks. Acquisition is strictly FIFO:
// a queued request is never overtaken by a later, smaller request, even if
// the smaller one would fit the available stock. Cancellation of the queue
// head advances the queue immediately. When a grant and a cancellation race,
// exactly one of them wins. Capacity may be resized at runtime; shrinking
// never revokes quotas that have already been granted, and any excess stock
// returned afterwards is absorbed against the deficit instead of becoming
// grantable again.
package budgetsem

import (
	"container/list"
	"context"
	"errors"
	"sync"
)

// ErrInvalidSize is returned for non-positive acquisition or release sizes.
var ErrInvalidSize = errors.New("budgetsem: size must be positive")

type waiterState int

const (
	statePending waiterState = iota
	stateGranted
	stateCancelled
)

type waiter struct {
	n     int64
	state waiterState
	ch    chan struct{} // closed exactly once when the grant is decided
}

// Semaphore is a strictly-FIFO byte-budget semaphore.
type Semaphore struct {
	mu       sync.Mutex
	capacity int64
	stock    int64     // grantable bytes; may be negative after a shrink
	queue    list.List // of *waiter, FIFO
}

// NewSemaphore returns a Semaphore with the given byte capacity.
func NewSemaphore(capacity int64) *Semaphore {
	if capacity < 0 {
		capacity = 0
	}
	return &Semaphore{capacity: capacity, stock: capacity}
}

// Acquire blocks until n bytes of budget are granted to the caller or ctx
// is cancelled. On success the caller owns n bytes and must eventually
// return them with Release. If a grant and a cancellation happen
// concurrently, the grant wins and Acquire returns nil.
func (s *Semaphore) Acquire(ctx context.Context, n int64) error {
	if n <= 0 {
		return ErrInvalidSize
	}
	s.mu.Lock()
	if s.queue.Len() == 0 && s.stock >= n {
		s.stock -= n
		s.mu.Unlock()
		return nil
	}
	w := &waiter{n: n, ch: make(chan struct{})}
	elem := s.queue.PushBack(w)
	s.mu.Unlock()

	select {
	case <-w.ch:
		return nil
	case <-ctx.Done():
		s.mu.Lock()
		if w.state == statePending {
			w.state = stateCancelled
			s.queue.Remove(elem)
			// The cancelled waiter may have been the head blocking
			// the queue; try to advance whoever is next.
			s.advanceLocked()
			s.mu.Unlock()
			return ctx.Err()
		}
		// The grant was already decided under the lock; it wins.
		s.mu.Unlock()
		return nil
	}
}

// Release returns n previously acquired bytes to the semaphore and grants
// budget to queued waiters in FIFO order.
func (s *Semaphore) Release(n int64) error {
	if n <= 0 {
		return ErrInvalidSize
	}
	s.mu.Lock()
	s.stock += n
	s.advanceLocked()
	s.mu.Unlock()
	return nil
}

// SetCapacity resizes the semaphore. Shrinking never revokes grants that
// are already outstanding; the stock simply goes negative and later
// releases are absorbed against the deficit until it is repaid.
func (s *Semaphore) SetCapacity(capacity int64) {
	if capacity < 0 {
		capacity = 0
	}
	s.mu.Lock()
	s.stock += capacity - s.capacity
	s.capacity = capacity
	s.advanceLocked()
	s.mu.Unlock()
}

// Capacity reports the current configured capacity.
func (s *Semaphore) Capacity() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.capacity
}

// Stock reports the currently grantable bytes. It is negative when a shrink
// made outstanding grants exceed the capacity.
func (s *Semaphore) Stock() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stock
}

// Waiters reports how many requests are queued. Intended for tests and
// observability.
func (s *Semaphore) Waiters() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.queue.Len()
}

// advanceLocked grants the queue head while it fits the available stock.
// Strict FIFO: a head request that does not fit blocks the whole queue, so
// no later request can overtake it.
func (s *Semaphore) advanceLocked() {
	for s.queue.Len() > 0 {
		elem := s.queue.Front()
		w := elem.Value.(*waiter)
		if w.state != statePending || s.stock < w.n {
			return
		}
		s.stock -= w.n
		w.state = stateGranted
		s.queue.Remove(elem)
		close(w.ch)
	}
}
