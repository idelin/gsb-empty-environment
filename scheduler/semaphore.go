package scheduler

import (
	"context"
	"sync"
)

// fairSemaphore is a counting semaphore with strict FIFO admission. Waiting
// acquisitions queue in order and can be abandoned via context cancellation
// without consuming a slot, which is the mechanism the Scheduler uses to tell
// "still queued" cancellations apart from verifications that have started.
type fairSemaphore struct {
	mu      sync.Mutex
	cap     int
	used    int
	waiters []*waiter
}

type waiter struct {
	ready chan struct{}
}

func newFairSemaphore(capacity int) *fairSemaphore {
	return &fairSemaphore{cap: capacity}
}

// tryAcquire takes a slot immediately when one is free and nobody is queued.
// It returns false when the caller must queue.
func (s *fairSemaphore) tryAcquire() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.used < s.cap && len(s.waiters) == 0 {
		s.used++
		return true
	}
	return false
}

// Acquire blocks until a slot is granted or ctx is canceled while queued.
// A canceled return never consumes a slot.
func (s *fairSemaphore) Acquire(ctx context.Context) error {
	if s.tryAcquire() {
		return nil
	}

	w := &waiter{ready: make(chan struct{})}
	s.mu.Lock()
	// Re-check under the lock in case a slot was released before we queued.
	if s.used < s.cap && len(s.waiters) == 0 {
		s.used++
		s.mu.Unlock()
		return nil
	}
	s.waiters = append(s.waiters, w)
	s.mu.Unlock()

	select {
	case <-w.ready:
		// The slot was granted by Release before any cancellation.
		return nil
	case <-ctx.Done():
		s.removeWaiter(w)
		return ctx.Err()
	}
}

// removeWaiter drops a queued waiter. If Release already granted it a slot
// concurrently, the granted slot is handed back so accounting stays balanced.
func (s *fairSemaphore) removeWaiter(w *waiter) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, q := range s.waiters {
		if q == w {
			s.waiters = append(s.waiters[:i], s.waiters[i+1:]...)
			return
		}
	}

	// Not in the queue anymore: Release already closed ready and consumed a slot
	// on this waiter's behalf, so return it.
	s.used--
}

// Release hands a slot back, granting it to the oldest queued waiter when one
// exists instead of making it immediately available.
func (s *fairSemaphore) Release() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.waiters) > 0 {
		// Grant the slot to the head waiter; FIFO is preserved because both
		// Release and removeWaiter run under s.mu, so an abandoned waiter is
		// either still in the queue (and skipped below) or has already handed
		// the slot back, in which case it is absent from the queue.
		head := s.waiters[0]
		s.waiters = s.waiters[1:]
		close(head.ready)
		return
	}

	s.used--
}

// inflight reports how many slots are currently held.
func (s *fairSemaphore) inflight() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.used
}

// queued reports how many callers are waiting for a slot.
func (s *fairSemaphore) queued() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.waiters)
}
