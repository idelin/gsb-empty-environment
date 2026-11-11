package netsec

import (
	"context"
	"sync"
)

// Backend describes one candidate backend address handed out by an
// Enumerator. It is intentionally minimal so the component can embed into any
// proxy configuration.
type Backend struct {
	Addr string
}

// Enumerator is a pausable, resumable source of backends. Next serves
// candidates one at a time in order; while paused, every Next blocks until
// Resume is called or the context expires. The pause point lets tests (and a
// real proxy) interleave stream startup, reset and cancellation precisely,
// instead of racing against an asynchronous dialer.
type Enumerator struct {
	mu       sync.Mutex
	cond     *sync.Cond
	backends []Backend
	idx      int
	paused   bool

	waiting int // Next calls currently blocked in the pause gate

	started  int // candidates ever handed out
	canceled int // Next calls that left via context cancellation
}

// NewEnumerator builds an Enumerator over the given backend list.
func NewEnumerator(backends []Backend) *Enumerator {
	e := &Enumerator{backends: append([]Backend(nil), backends...)}
	e.cond = sync.NewCond(&e.mu)
	return e
}

// Pause blocks subsequent Next calls at the enumeration gate. Candidates
// already handed out are unaffected.
func (e *Enumerator) Pause() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.paused = true
}

// Resume releases every Next call blocked at the pause gate.
func (e *Enumerator) Resume() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.paused = false
	e.cond.Broadcast()
}

// Pending returns the number of Next calls blocked at the pause gate.
func (e *Enumerator) Pending() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.waiting
}

// Started returns how many candidates have been handed out.
func (e *Enumerator) Started() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.started
}

// Canceled returns how many Next calls ended via context cancellation.
func (e *Enumerator) Canceled() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.canceled
}

// waitPauseGate blocks while the enumerator is paused. Cancellation is
// handled without spawning a goroutine per waiter.
func (e *Enumerator) waitPauseGate(ctx context.Context) error {
	done := make(chan struct{})
	var watcherOnce sync.Once
	go func() {
		select {
		case <-ctx.Done():
			e.mu.Lock()
			e.cond.Broadcast()
			e.mu.Unlock()
		case <-done:
		}
	}()
	defer watcherOnce.Do(func() { close(done) })

	e.mu.Lock()
	defer e.mu.Unlock()
	for e.paused {
		if err := ctx.Err(); err != nil {
			e.canceled++
			return err
		}
		e.waiting++
		e.cond.Wait()
		e.waiting--
		if err := ctx.Err(); err != nil {
			e.canceled++
			return err
		}
	}
	return nil
}

// Next returns the next backend in order, honoring pause and cancellation.
// When the list is exhausted it returns context.Canceled-style zero value with
// err == ErrExhausted. A canceled Next consumes no candidate.
func (e *Enumerator) Next(ctx context.Context) (Backend, error) {
	if err := e.waitPauseGate(ctx); err != nil {
		return Backend{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.idx >= len(e.backends) {
		return Backend{}, ErrExhausted
	}
	b := e.backends[e.idx]
	e.idx++
	e.started++
	return b, nil
}

// Reset rewinds the enumerator to the first backend. Blocked callers stay at
// the pause gate.
func (e *Enumerator) Reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.idx = 0
}
