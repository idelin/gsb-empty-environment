// Package reactive implements a small reactive signal graph with
// JSON checkpoint persistence and incremental hydration.
//
// A Signal is an observable value. An Effect re-runs automatically
// whenever any Signal it read during its last execution changes.
// A Computed is a derived value backed by an Effect.
//
// Signals whose name carries the "@persist" annotation are registered
// with the Store and serialized into checkpoints; all other signals
// are transient and never leave the process.
package reactive

import "sync"

// dependency is implemented by any signal a Signal[T] an Effect can
// subscribe to.
type dependency interface {
	unsubscribe(*Effect)
}

// observer stack used for automatic dependency tracking: while an
// Effect runs, every Signal.Get registers the Effect as a subscriber.
var tracking struct {
	sync.Mutex
	stack []*Effect
}

func pushObserver(e *Effect) {
	tracking.Lock()
	tracking.stack = append(tracking.stack, e)
	tracking.Unlock()
}

func popObserver() {
	tracking.Lock()
	tracking.stack = tracking.stack[:len(tracking.stack)-1]
	tracking.Unlock()
}

func currentObserver() *Effect {
	tracking.Lock()
	defer tracking.Unlock()
	if len(tracking.stack) == 0 {
		return nil
	}
	return tracking.stack[len(tracking.stack)-1]
}

// Signal is a reactive value of type T.
type Signal[T any] struct {
	mu    sync.RWMutex
	name  string
	value T
	subs  map[*Effect]struct{}
}

func newSignal[T any](name string, initial T) *Signal[T] {
	return &Signal[T]{name: name, value: initial, subs: make(map[*Effect]struct{})}
}

// Name returns the signal name without annotations.
func (s *Signal[T]) Name() string { return s.name }

// Get reads the value and, if called inside an Effect, subscribes
// that Effect to future changes.
func (s *Signal[T]) Get() T {
	if obs := currentObserver(); obs != nil {
		s.subscribe(obs)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.value
}

// Peek reads the value without subscribing the current Effect.
func (s *Signal[T]) Peek() T {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.value
}

// Set stores a new value and synchronously re-runs all subscribed
// Effects, which re-aligns the whole downstream dependency chain.
func (s *Signal[T]) Set(v T) {
	s.mu.Lock()
	s.value = v
	subs := make([]*Effect, 0, len(s.subs))
	for e := range s.subs {
		subs = append(subs, e)
	}
	s.mu.Unlock()
	for _, e := range subs {
		e.run()
	}
}

func (s *Signal[T]) subscribe(e *Effect) {
	s.mu.Lock()
	s.subs[e] = struct{}{}
	s.mu.Unlock()
	e.addDep(s)
}

func (s *Signal[T]) unsubscribe(e *Effect) {
	s.mu.Lock()
	delete(s.subs, e)
	s.mu.Unlock()
}

// Effect is a function that re-runs whenever any Signal read during
// its previous run changes. Dependencies are re-tracked on every run.
type Effect struct {
	fn       func()
	mu       sync.Mutex
	disposed bool
	depsMu   sync.Mutex
	deps     map[dependency]struct{}
}

// NewEffect registers fn and runs it immediately once.
func NewEffect(fn func()) *Effect {
	e := &Effect{fn: fn, deps: make(map[dependency]struct{})}
	e.run()
	return e
}

func (e *Effect) run() {
	e.mu.Lock()
	if e.disposed {
		e.mu.Unlock()
		return
	}
	e.mu.Unlock()

	// Detach from stale dependencies; they are re-tracked below.
	e.depsMu.Lock()
	for d := range e.deps {
		d.unsubscribe(e)
		delete(e.deps, d)
	}
	e.depsMu.Unlock()

	pushObserver(e)
	defer popObserver()
	e.fn()
}

func (e *Effect) addDep(d dependency) {
	e.depsMu.Lock()
	e.deps[d] = struct{}{}
	e.depsMu.Unlock()
}

// Dispose detaches the Effect from all Signals permanently.
func (e *Effect) Dispose() {
	e.mu.Lock()
	e.disposed = true
	e.mu.Unlock()
	e.depsMu.Lock()
	for d := range e.deps {
		d.unsubscribe(e)
	}
	e.deps = make(map[dependency]struct{})
	e.depsMu.Unlock()
}

// Computed is a derived, read-only reactive value. It recomputes
// whenever its own dependencies change and propagates downstream.
type Computed[T any] struct {
	sig    *Signal[T]
	effect *Effect
}

// NewComputed derives a value from fn. The result is always transient:
// it is recomputed from source signals after hydration, never stored.
func NewComputed[T any](store *Store, name string, fn func() T) *Computed[T] {
	sig := newSignal[T](name, *new(T))
	c := &Computed[T]{sig: sig}
	c.effect = NewEffect(func() {
		sig.Set(fn())
	})
	return c
}

// Name returns the computed value's name.
func (c *Computed[T]) Name() string { return c.sig.Name() }

// Get reads the derived value, subscribing the current Effect.
func (c *Computed[T]) Get() T { return c.sig.Get() }

// Peek reads the derived value without subscribing.
func (c *Computed[T]) Peek() T { return c.sig.Peek() }
