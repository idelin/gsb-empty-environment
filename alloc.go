// Package mra implements a multi-resource allocator with all-or-nothing
// acquisition, cancellation, bounded-overtake fair queuing, release
// callbacks and dynamic quota management.
//
// Semantics:
//   - Acquire is all-or-nothing: a request is granted only when every
//     demanded resource can be satisfied simultaneously. A waiting request
//     never holds a partial allocation, so it can never block while
//     holding half of its resources.
//   - Requests are queued FIFO. The head of the queue is always considered
//     first. If the head cannot be satisfied, later requests that *can* be
//     satisfied may be granted ("skip"), but each such grant increments the
//     head's skip counter. Once the head has been skipped MaxSkips times,
//     no further overtaking is allowed: the head blocks the queue until it
//     is satisfied. This bounds overtaking and prevents starvation.
//   - Acquire is cancellable through context.Context.
//   - Release returns resources and runs the grant loop; the OnRelease
//     callback (if any) is invoked after the internal lock is dropped, so
//     the callback may safely re-enter the allocator (Acquire/Release/
//     SetQuota) without deadlocking.
//   - SetQuota may shrink a quota below the current usage. Existing
//     holders are unaffected; new grants are refused until usage drops
//     below the (new) quota.
package mra

import (
	"context"
	"errors"
	"sync"
)

// DefaultMaxSkips is the default number of times the queue head may be
// overtaken before it blocks further overtaking.
const DefaultMaxSkips = 3

var (
	// ErrExceedsQuota is returned by Acquire when a demand can never be
	// satisfied because it exceeds the configured quota of a resource.
	ErrExceedsQuota = errors.New("mra: demand exceeds quota")
	// ErrInvalidDemand is returned by Acquire for empty or non-positive
	// demands.
	ErrInvalidDemand = errors.New("mra: invalid demand")
)

// Demands maps a resource name to the amount requested/released.
type Demands map[string]int64

func cloneDemands(d Demands) Demands {
	c := make(Demands, len(d))
	for k, v := range d {
		c[k] = v
	}
	return c
}

type waiter struct {
	demands Demands
	skips   int
	ready   chan struct{} // closed exactly once when granted
	granted bool
}

// Allocator hands out multiple named resources under per-resource quotas.
type Allocator struct {
	mu    sync.Mutex
	quota map[string]int64
	used  map[string]int64
	queue []*waiter

	maxSkips  int
	onRelease func(Demands)
}

// Option configures an Allocator.
type Option func(*Allocator)

// WithMaxSkips sets how many times the queue head may be overtaken before
// it blocks the queue. Values < 0 are treated as 0 (strict FIFO).
func WithMaxSkips(n int) Option {
	return func(a *Allocator) {
		if n < 0 {
			n = 0
		}
		a.maxSkips = n
	}
}

// WithOnRelease registers a callback invoked (without the internal lock
// held) after every Release. The callback receives a copy of the released
// demands. It may safely re-enter the allocator. It must not block
// indefinitely: it runs on the releaser's goroutine.
func WithOnRelease(cb func(Demands)) Option {
	return func(a *Allocator) { a.onRelease = cb }
}

// New creates an Allocator with the given per-resource quotas.
func New(quota map[string]int64, opts ...Option) *Allocator {
	a := &Allocator{
		quota:    make(map[string]int64, len(quota)),
		used:     make(map[string]int64, len(quota)),
		maxSkips: DefaultMaxSkips,
	}
	for r, q := range quota {
		a.quota[r] = q
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// Acquire blocks until all demands are granted atomically, the context is
// cancelled, or the demand is provably infeasible (ErrExceedsQuota /
// ErrInvalidDemand). On success the caller owns the demanded resources and
// must return them with Release. If the context is cancelled the request
// is dequeued and no resources are held.
func (a *Allocator) Acquire(ctx context.Context, d Demands) error {
	a.mu.Lock()
	if err := a.checkFeasibleLocked(d); err != nil {
		a.mu.Unlock()
		return err
	}
	w := &waiter{demands: cloneDemands(d), ready: make(chan struct{})}
	a.queue = append(a.queue, w)
	a.grantLocked()
	if w.granted {
		a.mu.Unlock()
		return nil
	}
	a.mu.Unlock()

	select {
	case <-w.ready:
		return nil
	case <-ctx.Done():
		a.mu.Lock()
		if w.granted {
			// Lost the race against a grant: the caller now owns the
			// resources and must release them.
			a.mu.Unlock()
			return nil
		}
		a.removeLocked(w)
		a.mu.Unlock()
		return ctx.Err()
	}
}

// Release returns previously acquired resources, wakes eligible waiters,
// then invokes the OnRelease callback (if any) without holding the lock.
// Releasing more than is held for a resource panics: it is a bug.
func (a *Allocator) Release(d Demands) {
	a.mu.Lock()
	for r, n := range d {
		if n <= 0 {
			continue
		}
		a.used[r] -= n
		if a.used[r] < 0 {
			a.mu.Unlock()
			panic("mra: release underflow on resource " + r)
		}
	}
	a.grantLocked()
	cb := a.onRelease
	a.mu.Unlock()
	if cb != nil {
		cb(cloneDemands(d))
	}
}

// SetQuota changes the quota of a resource. Shrinking below current usage
// is allowed: outstanding holders keep their resources, but no new grants
// are made for that resource until usage drops below the new quota.
func (a *Allocator) SetQuota(resource string, q int64) {
	a.mu.Lock()
	a.quota[resource] = q
	a.grantLocked()
	a.mu.Unlock()
}

// Usage returns the currently allocated amount of a resource.
func (a *Allocator) Usage(resource string) int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.used[resource]
}

// Quota returns the configured quota of a resource.
func (a *Allocator) Quota(resource string) int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.quota[resource]
}

// QueueLen returns the number of waiting (ungranted) requests.
func (a *Allocator) QueueLen() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for _, w := range a.queue {
		if !w.granted {
			n++
		}
	}
	return n
}

// checkFeasibleLocked rejects demands that are malformed or can never be
// satisfied under the current quota.
func (a *Allocator) checkFeasibleLocked(d Demands) error {
	if len(d) == 0 {
		return ErrInvalidDemand
	}
	for r, n := range d {
		if n <= 0 {
			return ErrInvalidDemand
		}
		if n > a.quota[r] {
			return ErrExceedsQuota
		}
	}
	return nil
}

// fitsLocked reports whether d can be granted with current free capacity.
func (a *Allocator) fitsLocked(d Demands) bool {
	for r, n := range d {
		if a.used[r]+n > a.quota[r] {
			return false
		}
	}
	return true
}

// commitLocked grants w: deducts resources and wakes the waiter.
func (a *Allocator) commitLocked(w *waiter) {
	for r, n := range w.demands {
		a.used[r] += n
	}
	w.granted = true
	close(w.ready)
}

// grantLocked runs the scheduling loop: satisfy the head if possible,
// otherwise overtake with satisfiable later requests up to the head's
// skip budget. Must be called with a.mu held after every state change.
func (a *Allocator) grantLocked() {
	for len(a.queue) > 0 {
		head := a.queue[0]
		if a.fitsLocked(head.demands) {
			a.commitLocked(head)
			a.queue = a.queue[1:]
			continue
		}
		if head.skips >= a.maxSkips {
			return // head-of-line block: bounded overtaking exhausted
		}
		idx := -1
		for i := 1; i < len(a.queue); i++ {
			if a.fitsLocked(a.queue[i].demands) {
				idx = i
				break
			}
		}
		if idx < 0 {
			return
		}
		w := a.queue[idx]
		a.queue = append(a.queue[:idx], a.queue[idx+1:]...)
		a.commitLocked(w)
		head.skips++
	}
}

func (a *Allocator) removeLocked(w *waiter) {
	for i, q := range a.queue {
		if q == w {
			a.queue = append(a.queue[:i], a.queue[i+1:]...)
			return
		}
	}
}
