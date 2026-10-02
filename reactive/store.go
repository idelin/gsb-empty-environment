// Package reactive implements a persistent reactive store: an in-memory
// Signal state tree with automatic dependency tracking, where only Signals
// annotated with @persist are serialized into JSON checkpoints, and an
// incremental hydrator restores them after a process restart while derived
// Effects automatically re-align.
package reactive

import "fmt"

// PersistAnnotation is the annotation name marking a Signal as durable.
// It is the Go mapping of the "@persist" annotation from the spec.
const PersistAnnotation = "persist"

// Option annotates a Signal at registration time.
type Option func(*Signal)

// Persist marks a Signal with the @persist annotation, so it is included
// in checkpoints and restored during hydration.
func Persist() Option {
	return func(s *Signal) { s.annotations[PersistAnnotation] = true }
}

// Annotate attaches an arbitrary annotation to a Signal.
func Annotate(name string) Option {
	return func(s *Signal) { s.annotations[name] = true }
}

// Signal is a single reactive state cell in the store.
type Signal struct {
	store       *Store
	name        string
	value       any
	annotations map[string]bool
	subs        map[*Effect]struct{}
}

// Name returns the Signal's unique name within its Store.
func (s *Signal) Name() string { return s.name }

// HasAnnotation reports whether the Signal carries the given annotation.
func (s *Signal) HasAnnotation(name string) bool { return s.annotations[name] }

// Persistent reports whether the Signal carries the @persist annotation.
func (s *Signal) Persistent() bool { return s.HasAnnotation(PersistAnnotation) }

// Get reads the current value. When called inside a running Effect, the
// Effect is subscribed to this Signal automatically (dependency tracking).
func (s *Signal) Get() any {
	if active := s.store.active; active != nil {
		active.deps[s] = struct{}{}
		s.subs[active] = struct{}{}
	}
	return s.value
}

// Peek reads the current value without dependency tracking.
func (s *Signal) Peek() any { return s.value }

// Set writes a new value and synchronously re-runs all subscribed Effects,
// which propagates through the whole dependency chain.
func (s *Signal) Set(v any) {
	s.value = v
	// Collect first: re-running an Effect mutates subscription sets.
	subs := make([]*Effect, 0, len(s.subs))
	for e := range s.subs {
		subs = append(subs, e)
	}
	for _, e := range subs {
		e.run()
	}
}

// Effect is a derived computation. It re-runs whenever any Signal it read
// during its last execution changes. Dependencies are re-tracked on every
// run, so dynamic dependency graphs stay correct.
type Effect struct {
	store *Store
	fn    func()
	deps  map[*Signal]struct{}
	runs  int
}

// Runs reports how many times the Effect has executed.
func (e *Effect) Runs() int { return e.runs }

func (e *Effect) run() {
	// Detach from stale dependencies before re-tracking.
	for dep := range e.deps {
		delete(dep.subs, e)
	}
	e.deps = make(map[*Signal]struct{})

	prev := e.store.active
	e.store.active = e
	defer func() { e.store.active = prev }()

	e.fn()
	e.runs++
}

// Store is the in-memory reactive state tree. It is designed for a single
// goroutine (the typical UI/state-loop model); no internal locking is done.
type Store struct {
	signals map[string]*Signal
	effects []*Effect
	active  *Effect // Effect currently evaluating, for dependency tracking
}

// NewStore creates an empty reactive store.
func NewStore() *Store {
	return &Store{signals: make(map[string]*Signal)}
}

// Signal registers (or returns) a named Signal with an initial value.
// Registering an existing name again returns the existing Signal and
// ignores the new initial value and options.
func (st *Store) Signal(name string, initial any, opts ...Option) *Signal {
	if sig, ok := st.signals[name]; ok {
		return sig
	}
	sig := &Signal{
		store:       st,
		name:        name,
		value:       initial,
		annotations: make(map[string]bool),
		subs:        make(map[*Effect]struct{}),
	}
	for _, opt := range opts {
		opt(sig)
	}
	st.signals[name] = sig
	return sig
}

// Lookup returns a registered Signal by name, or nil if absent.
func (st *Store) Lookup(name string) *Signal { return st.signals[name] }

// Effect registers a derived computation and runs it immediately once.
func (st *Store) Effect(fn func()) *Effect {
	e := &Effect{store: st, fn: fn, deps: make(map[*Signal]struct{})}
	st.effects = append(st.effects, e)
	e.run()
	return e
}

// Computed creates a derived Signal whose value is kept up to date by an
// internal Effect re-evaluating fn whenever its dependencies change.
func (st *Store) Computed(name string, fn func() any, opts ...Option) *Signal {
	sig := st.Signal(name, nil, opts...)
	st.Effect(func() { sig.Set(fn()) })
	return sig
}

// Signals returns all registered Signals in deterministic (name) order.
func (st *Store) Signals() []*Signal {
	names := st.sortedNames()
	out := make([]*Signal, 0, len(names))
	for _, n := range names {
		out = append(out, st.signals[n])
	}
	return out
}

func (st *Store) sortedNames() []string {
	names := make([]string, 0, len(st.signals))
	for n := range st.signals {
		names = append(names, n)
	}
	// insertion-agnostic deterministic order
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] < names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
	return names
}

// String renders a debug view of the store's current state.
func (st *Store) String() string {
	out := "Store{\n"
	for _, sig := range st.Signals() {
		tag := "transient"
		if sig.Persistent() {
			tag = "@persist"
		}
		out += fmt.Sprintf("  %-16s %-10s %v\n", sig.name+":", tag, sig.Peek())
	}
	return out + "}"
}
