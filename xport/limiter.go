package xport

import "sync"

// MemLimiter is a counting semaphore over bytes. It provides backpressure
// so that buffered change data never exceeds a configured memory budget.
type MemLimiter struct {
	mu    sync.Mutex
	cond  *sync.Cond
	limit int64
	used  int64
	peak  int64
}

// NewMemLimiter returns a limiter allowing at most limit bytes in flight.
func NewMemLimiter(limit int64) *MemLimiter {
	l := &MemLimiter{limit: limit}
	l.cond = sync.NewCond(&l.mu)
	return l
}

// Acquire blocks until n bytes can be reserved within the budget.
func (l *MemLimiter) Acquire(n int64) {
	l.mu.Lock()
	for l.used+n > l.limit {
		l.cond.Wait()
	}
	l.used += n
	if l.used > l.peak {
		l.peak = l.used
	}
	l.mu.Unlock()
}

// TryAcquire reserves n bytes only if it can do so without blocking.
func (l *MemLimiter) TryAcquire(n int64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.used+n > l.limit {
		return false
	}
	l.used += n
	if l.used > l.peak {
		l.peak = l.used
	}
	return true
}

// Release returns n bytes to the budget and wakes blocked producers.
func (l *MemLimiter) Release(n int64) {
	l.mu.Lock()
	l.used -= n
	if l.used < 0 {
		l.used = 0
	}
	l.cond.Broadcast()
	l.mu.Unlock()
}

// Peak reports the maximum bytes ever reserved concurrently.
func (l *MemLimiter) Peak() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.peak
}

// Limit returns the configured budget.
func (l *MemLimiter) Limit() int64 { return l.limit }
