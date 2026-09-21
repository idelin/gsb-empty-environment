// Package aimd implements an adaptive concurrency limiter driven by
// explicit latency/timeout samples using additive-increase
// multiplicative-decrease (AIMD) rules.
package aimd

import (
	"context"
	"sync"
	"time"
)

// Sample is one observed task outcome, injected by the caller.
type Sample struct {
	RTT     time.Duration
	Timeout bool
}

// Options configures a Limiter. Zero fields get defaults.
type Options struct {
	InitialLimit int           // default: MinLimit
	MinLimit     int           // default: 1
	MaxLimit     int           // default: 64
	Additive     int           // additive increase step, default: 1
	Backoff      float64       // multiplicative decrease factor, default: 0.5
	Debounce     time.Duration // min quiet time after a decrease before increasing again, default: 200ms
	Clock        Clock         // default: real clock
}

// Limiter is a dynamic-capacity semaphore. Lowering the limit never
// kills in-flight tasks; they finish normally and no new task is
// admitted (no backfill) until running < limit again.
type Limiter struct {
	mu           sync.Mutex
	changed      chan struct{} // closed+replaced on every state change
	limit        int
	running      int
	min          int
	max          int
	additive     int
	backoff      float64
	debounce     time.Duration
	lastDecrease time.Time
	clock        Clock
}

func NewLimiter(opt Options) *Limiter {
	if opt.MinLimit <= 0 {
		opt.MinLimit = 1
	}
	if opt.MaxLimit <= 0 {
		opt.MaxLimit = 64
	}
	if opt.Additive <= 0 {
		opt.Additive = 1
	}
	if opt.Backoff <= 0 || opt.Backoff >= 1 {
		opt.Backoff = 0.5
	}
	if opt.Debounce <= 0 {
		opt.Debounce = 200 * time.Millisecond
	}
	if opt.Clock == nil {
		opt.Clock = realClock{}
	}
	initial := opt.InitialLimit
	if initial <= 0 {
		initial = opt.MinLimit
	}
	if initial < opt.MinLimit {
		initial = opt.MinLimit
	}
	if initial > opt.MaxLimit {
		initial = opt.MaxLimit
	}
	return &Limiter{
		changed:  make(chan struct{}),
		limit:    initial,
		min:      opt.MinLimit,
		max:      opt.MaxLimit,
		additive: opt.Additive,
		backoff:  opt.Backoff,
		debounce: opt.Debounce,
		clock:    opt.Clock,
	}
}

// Limit returns the current concurrency limit.
func (l *Limiter) Limit() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.limit
}

// Running returns the number of in-flight tasks.
func (l *Limiter) Running() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.running
}

// Adjust applies AIMD to a batch of samples and returns (old, new) limit.
// An empty batch changes nothing. Any timeout in the batch triggers one
// multiplicative decrease; otherwise the limit grows additively, unless
// we are still inside the debounce window after the last decrease.
func (l *Limiter) Adjust(samples []Sample) (int, int) {
	if len(samples) == 0 {
		cur := l.Limit()
		return cur, cur
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	old := l.limit
	timeout := false
	for _, s := range samples {
		if s.Timeout {
			timeout = true
			break
		}
	}
	if timeout {
		next := int(float64(l.limit) * l.backoff)
		if next < l.min {
			next = l.min
		}
		l.limit = next
		l.lastDecrease = l.clock.Now()
	} else if l.clock.Now().Sub(l.lastDecrease) >= l.debounce {
		l.limit += l.additive
		if l.limit > l.max {
			l.limit = l.max
		}
	}
	if l.limit != old {
		l.broadcastLocked()
	}
	return old, l.limit
}

// Acquire blocks until running < limit or ctx is done. The returned
// release func is idempotent and must be called when the task ends.
func (l *Limiter) Acquire(ctx context.Context) (release func(), err error) {
	l.mu.Lock()
	for l.running >= l.limit {
		ch := l.changed
		l.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ch:
		}
		l.mu.Lock()
	}
	l.running++
	l.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			l.running--
			l.broadcastLocked()
			l.mu.Unlock()
		})
	}, nil
}

// Run acquires a slot, runs fn, and releases the slot.
func (l *Limiter) Run(ctx context.Context, fn func(context.Context) error) error {
	release, err := l.Acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	return fn(ctx)
}

func (l *Limiter) broadcastLocked() {
	close(l.changed)
	l.changed = make(chan struct{})
}
