// Package aimd implements an adaptive concurrency limiter that adjusts its
// limit from explicit latency/timeout samples using additive-increase
// multiplicative-decrease (AIMD).
package aimd

import (
	"sync"
	"time"
)

// Sample is one observed task outcome. Latency is the explicit task latency
// reported by the caller; Timeout marks a timed-out task.
type Sample struct {
	Latency time.Duration
	Timeout bool
}

// Limiter gates how many tasks may run concurrently. Reducing the limit
// never kills in-flight tasks: they run to completion, and new acquisitions
// simply block until inflight drops below the reduced limit.
type Limiter struct {
	mu       sync.Mutex
	cond     *sync.Cond
	clock    Clock
	limit    int
	inflight int

	minLimit int
	maxLimit int
	backoff  float64 // multiplicative decrease factor, e.g. 0.5

	// minInterval debounces adjustments: after any limit change, further
	// changes are ignored until this much (clock) time has elapsed, which
	// stabilizes the recovery phase.
	minInterval time.Duration
	lastAdjust  time.Time
}

func NewLimiter(initial, minLimit, maxLimit int, backoff float64, minInterval time.Duration, clock Clock) *Limiter {
	if initial < minLimit {
		initial = minLimit
	}
	if initial > maxLimit {
		initial = maxLimit
	}
	l := &Limiter{
		clock:       clock,
		limit:       initial,
		minLimit:    minLimit,
		maxLimit:    maxLimit,
		backoff:     backoff,
		minInterval: minInterval,
		lastAdjust:  clock.Now(),
	}
	l.cond = sync.NewCond(&l.mu)
	return l
}

// Acquire blocks until a concurrency slot is free, then takes it.
func (l *Limiter) Acquire() {
	l.mu.Lock()
	for l.inflight >= l.limit {
		l.cond.Wait()
	}
	l.inflight++
	l.mu.Unlock()
}

// Release returns a slot taken by Acquire. It never panics on the limit
// having shrunk underneath: in-flight tasks always finish normally.
func (l *Limiter) Release() {
	l.mu.Lock()
	l.inflight--
	l.cond.Broadcast()
	l.mu.Unlock()
}

// Update applies the AIMD rule to a batch of explicit samples:
//   - empty batch: no adjustment;
//   - any timeout: limit = limit * backoff (multiplicative decrease, floored
//     at minLimit);
//   - otherwise: limit += 1 (additive increase, capped at maxLimit).
//
// Adjustments are debounced: calls arriving within minInterval of the last
// applied adjustment are ignored.
func (l *Limiter) Update(samples []Sample) {
	if len(samples) == 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock.Now()
	if now.Sub(l.lastAdjust) < l.minInterval {
		return
	}
	timeout := false
	for _, s := range samples {
		if s.Timeout {
			timeout = true
			break
		}
	}
	if timeout {
		decreased := int(float64(l.limit) * l.backoff)
		if decreased < l.minLimit {
			decreased = l.minLimit
		}
		l.limit = decreased
	} else if l.limit < l.maxLimit {
		l.limit++
	}
	l.lastAdjust = now
	l.cond.Broadcast()
}

func (l *Limiter) Limit() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.limit
}

func (l *Limiter) InFlight() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.inflight
}
