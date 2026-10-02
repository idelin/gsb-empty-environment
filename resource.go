// Package suspense implements a reactive async resource binder:
// a Suspense-style state machine (Pending / Success / Error) bound to
// asynchronous promises, with stale-request invalidation on races.
package suspense

import (
	"context"
	"sync"
)

// State is the Suspense state machine's node.
type State int

const (
	Pending State = iota // request in flight, data not ready
	Success              // data ready
	Error                // request failed
)

func (s State) String() string {
	switch s {
	case Pending:
		return "Pending"
	case Success:
		return "Success"
	case Error:
		return "Error"
	}
	return "Unknown"
}

// Snapshot is an immutable view of a Resource at a point in time,
// propagated downstream on every state transition.
type Snapshot[R any] struct {
	State State
	Value R
	Err   error
	Epoch uint64 // monotonic request generation; larger wins
}

// Signal is a reactive value. Mutating it notifies subscribers
// synchronously, in subscription order.
type Signal[T any] struct {
	mu     sync.RWMutex
	val    T
	subs   map[int]func(T)
	nextID int
}

// NewSignal creates a Signal holding an initial value.
func NewSignal[T any](initial T) *Signal[T] {
	return &Signal[T]{val: initial, subs: make(map[int]func(T))}
}

// Get reads the current value.
func (s *Signal[T]) Get() T {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.val
}

// Set stores a new value and notifies all subscribers.
func (s *Signal[T]) Set(v T) {
	s.mu.Lock()
	s.val = v
	subs := make([]func(T), 0, len(s.subs))
	for _, fn := range s.subs {
		subs = append(subs, fn)
	}
	s.mu.Unlock()
	for _, fn := range subs {
		fn(v)
	}
}

// Subscribe registers fn to be called on every Set. Returns an unsubscribe func.
func (s *Signal[T]) Subscribe(fn func(T)) (unsub func()) {
	s.mu.Lock()
	id := s.nextID
	s.nextID++
	s.subs[id] = fn
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		delete(s.subs, id)
		s.mu.Unlock()
	}
}

// Fetcher resolves a query into a result. It must honor ctx cancellation.
type Fetcher[P, R any] func(ctx context.Context, query P) (R, error)

// Resource binds a source Signal to an async Fetcher.
//
// State machine per request generation:
//
//	Set(query) ──▶ Pending ──▶ Success (fetch resolved, epoch current)
//	                   │
//	                   └─────▶ Error   (fetch failed,  epoch current)
//
// A newer Set() bumps the epoch, cancels the previous fetch's context and
// re-enters Pending. Late completions from stale epochs are discarded, so
// downstream never observes an out-of-date result.
type Resource[P, R any] struct {
	mu     sync.Mutex // guards state fields
	nMu    sync.Mutex // serializes transition+notify so downstream sees a total order
	epoch  uint64
	cancel context.CancelFunc // cancels the in-flight fetch
	snap   Snapshot[R]
	subs   map[int]func(Snapshot[R])
	nextID int
	wg     sync.WaitGroup // tracks in-flight fetch goroutines
	closed bool

	unsubSrc func()
}

// NewResource creates a Resource driven by src. The initial fetch for
// src.Get() starts immediately in Pending state.
func NewResource[P, R any](ctx context.Context, src *Signal[P], fetch Fetcher[P, R]) *Resource[P, R] {
	r := &Resource[P, R]{subs: make(map[int]func(Snapshot[R]))}
	r.unsubSrc = src.Subscribe(func(q P) { r.reload(ctx, q, fetch) })
	r.reload(ctx, src.Get(), fetch)
	return r
}

// reload is the state-machine entry point for a new query.
func (r *Resource[P, R]) reload(parent context.Context, q P, fetch Fetcher[P, R]) {
	r.nMu.Lock()
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		r.nMu.Unlock()
		return
	}
	if r.cancel != nil {
		r.cancel() // invalidate the previous in-flight request
	}
	r.epoch++
	epoch := r.epoch
	ctx, cancel := context.WithCancel(parent)
	r.cancel = cancel
	r.snap = Snapshot[R]{State: Pending, Epoch: epoch}
	snap := r.snap
	subs := r.collectSubsLocked()
	r.mu.Unlock()
	notify(subs, snap) // propagate the Pending beacon downstream
	r.nMu.Unlock()

	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		val, err := fetch(ctx, q)

		r.nMu.Lock()
		r.mu.Lock()
		if r.closed || epoch != r.epoch {
			// Stale promise: a newer request superseded this one. Discard.
			r.mu.Unlock()
			r.nMu.Unlock()
			return
		}
		if err != nil {
			r.snap = Snapshot[R]{State: Error, Err: err, Epoch: epoch}
		} else {
			r.snap = Snapshot[R]{State: Success, Value: val, Epoch: epoch}
		}
		snap := r.snap
		subs := r.collectSubsLocked()
		r.mu.Unlock()
		notify(subs, snap)
		r.nMu.Unlock()
	}()
}

func (r *Resource[P, R]) collectSubsLocked() []func(Snapshot[R]) {
	subs := make([]func(Snapshot[R]), 0, len(r.subs))
	for _, fn := range r.subs {
		subs = append(subs, fn)
	}
	return subs
}

func notify[R any](subs []func(Snapshot[R]), snap Snapshot[R]) {
	for _, fn := range subs {
		fn(snap)
	}
}

// Snapshot returns the current immutable state.
func (r *Resource[P, R]) Snapshot() Snapshot[R] {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.snap
}

// Subscribe registers fn to receive every downstream state transition in
// order. The Pending beacon is emitted synchronously on each new query;
// terminal states (Success/Error) are emitted from the fetch goroutine.
// fn may safely call Snapshot.
func (r *Resource[P, R]) Subscribe(fn func(Snapshot[R])) (unsub func()) {
	r.mu.Lock()
	id := r.nextID
	r.nextID++
	r.subs[id] = fn
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		delete(r.subs, id)
		r.mu.Unlock()
	}
}

// Wait blocks until all in-flight fetches (including discarded stale ones)
// have completed. Useful for tests and graceful shutdown.
func (r *Resource[P, R]) Wait() { r.wg.Wait() }

// Close cancels the in-flight fetch and detaches from the source signal.
func (r *Resource[P, R]) Close() {
	r.nMu.Lock()
	r.mu.Lock()
	if !r.closed {
		r.closed = true
		if r.cancel != nil {
			r.cancel()
		}
	}
	r.mu.Unlock()
	r.nMu.Unlock()
	if r.unsubSrc != nil {
		r.unsubSrc()
	}
	r.wg.Wait()
}
