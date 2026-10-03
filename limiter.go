package bigtxn

import "sync"

// MemoryLimiter is a byte-budget semaphore guarding the transfer
// pipeline. Producers must reserve the estimated size of a chunk before
// handing it to the sink; when the budget is exhausted the caller spills
// to disk instead of growing memory, protecting the receiver from OOM.
type MemoryLimiter struct {
	mu     sync.Mutex
	cond   *sync.Cond
	budget int64
	used   int64
	peak   int64
	denied int64
}

// NewMemoryLimiter returns a limiter allowing at most budget bytes of
// chunk payload in flight at any moment.
func NewMemoryLimiter(budget int64) *MemoryLimiter {
	l := &MemoryLimiter{budget: budget}
	l.cond = sync.NewCond(&l.mu)
	return l
}

// TryReserve attempts to reserve n bytes without blocking. It returns
// false when the reservation would exceed the budget; the caller is
// expected to spill the chunk to disk.
func (l *MemoryLimiter) TryReserve(n int64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.used+n > l.budget {
		l.denied++
		return false
	}
	l.used += n
	if l.used > l.peak {
		l.peak = l.used
	}
	return true
}

// Acquire blocks until n bytes can be reserved. A reservation larger
// than the budget is granted as soon as the limiter is idle, so a single
// oversized chunk can always make progress.
func (l *MemoryLimiter) Acquire(n int64) {
	l.mu.Lock()
	for l.used > 0 && l.used+n > l.budget {
		l.cond.Wait()
	}
	l.used += n
	if l.used > l.peak {
		l.peak = l.used
	}
	l.mu.Unlock()
}

// Release returns n bytes to the budget.
func (l *MemoryLimiter) Release(n int64) {
	l.mu.Lock()
	l.used -= n
	if l.used < 0 {
		l.used = 0
	}
	l.cond.Broadcast()
	l.mu.Unlock()
}

// Peak reports the maximum bytes in flight observed so far.
func (l *MemoryLimiter) Peak() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.peak
}

// Denied reports how many TryReserve calls were rejected, i.e. how many
// times the pipeline was forced to spill.
func (l *MemoryLimiter) Denied() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.denied
}

// Budget returns the configured byte budget.
func (l *MemoryLimiter) Budget() int64 { return l.budget }
