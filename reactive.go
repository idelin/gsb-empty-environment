// Package reactive implements a small reactive system with transactional
// batching. Signals hold values, Computeds derive values lazily, and Effects
// run side effects. Inside a Batch block, all downstream Computeds and
// Effects stay silent; the cascade is flushed exactly once when the
// outermost batch exits normally.
package reactive

// source is anything an observer can depend on (Signal or Computed).
type source interface {
	subscribe(o observer)
	unsubscribe(o observer)
}

// observer is anything that can depend on sources (Computed or Effect).
type observer interface {
	addSource(src source)
	notify()
}

// Scope is a reactive context. All Signals, Computeds and Effects created
// against the same Scope share one dependency graph and one batch queue.
// A Scope is not safe for concurrent use from multiple goroutines.
type Scope struct {
	batchDepth int
	pending    []*Effect
	queued     map[*Effect]struct{}
	observer   observer // observer currently re-evaluating (dependency tracking)
}

// NewScope creates an empty reactive context.
func NewScope() *Scope {
	return &Scope{queued: make(map[*Effect]struct{})}
}

// Batch runs fn as one transaction. While fn executes, notifications are
// suspended: Computeds are only marked dirty and Effects are queued without
// running. When the outermost Batch returns normally, every queued Effect
// runs exactly once, observing the latest values. If fn panics, the queue
// is discarded and the panic propagates.
func (s *Scope) Batch(fn func()) {
	s.batchDepth++
	panicked := true
	defer func() {
		s.batchDepth--
		if panicked && s.batchDepth == 0 {
			s.discard()
		}
	}()
	fn()
	panicked = false
	if s.batchDepth == 1 {
		s.flush()
	}
}

// discard drops queued notifications without running them.
func (s *Scope) discard() {
	for _, e := range s.pending {
		e.queued = false
	}
	s.pending = nil
	clear(s.queued)
}

// flush runs queued effects in FIFO order until the queue drains. Effects
// enqueued by other effects during the flush are processed in the same pass.
func (s *Scope) flush() {
	for len(s.pending) > 0 {
		e := s.pending[0]
		s.pending = s.pending[1:]
		delete(s.queued, e)
		e.queued = false
		if !e.stopped {
			e.run()
		}
	}
}

// track records src as a dependency of the observer currently evaluating.
func (s *Scope) track(src source) {
	if s.observer != nil {
		s.observer.addSource(src)
	}
}

// enqueue schedules an effect, deduplicating so it runs at most once per flush.
func (s *Scope) enqueue(e *Effect) {
	if e.queued {
		return
	}
	e.queued = true
	s.queued[e] = struct{}{}
	s.pending = append(s.pending, e)
}

// Signal is a writable reactive value.
type Signal[T comparable] struct {
	scope *Scope
	value T
	subs  map[observer]struct{}
}

// NewSignal creates a Signal holding initial, bound to scope.
func NewSignal[T comparable](scope *Scope, initial T) *Signal[T] {
	return &Signal[T]{scope: scope, value: initial, subs: make(map[observer]struct{})}
}

// Get reads the current value and registers the calling observer (if any)
// as a subscriber.
func (sig *Signal[T]) Get() T {
	sig.scope.track(sig)
	return sig.value
}

// Set stores v. If v differs from the current value, every subscriber is
// notified: Computeds become dirty and Effects run immediately, or are
// queued when a Batch is in progress.
func (sig *Signal[T]) Set(v T) {
	if sig.value == v {
		return
	}
	sig.value = v
	for sub := range sig.subs {
		sub.notify()
	}
}

func (sig *Signal[T]) subscribe(o observer)   { sig.subs[o] = struct{}{} }
func (sig *Signal[T]) unsubscribe(o observer) { delete(sig.subs, o) }

// Computed is a lazily evaluated derived value. It recomputes only when one
// of its dependencies changed since the last read.
type Computed[T comparable] struct {
	scope   *Scope
	fn      func() T
	value   T
	dirty   bool
	subs    map[observer]struct{}
	sources map[source]struct{}
}

// NewComputed creates a Computed deriving its value from fn.
func NewComputed[T comparable](scope *Scope, fn func() T) *Computed[T] {
	return &Computed[T]{
		scope:   scope,
		fn:      fn,
		dirty:   true,
		subs:    make(map[observer]struct{}),
		sources: make(map[source]struct{}),
	}
}

// Get returns the cached value, recomputing first if any dependency changed.
func (c *Computed[T]) Get() T {
	c.scope.track(c)
	if c.dirty {
		c.recompute()
	}
	return c.value
}

func (c *Computed[T]) recompute() {
	for src := range c.sources {
		src.unsubscribe(c)
	}
	clear(c.sources)
	prev := c.scope.observer
	c.scope.observer = c
	c.value = c.fn()
	c.scope.observer = prev
	c.dirty = false
}

// notify marks the Computed dirty and propagates the change downstream.
func (c *Computed[T]) notify() {
	if c.dirty {
		return
	}
	c.dirty = true
	for sub := range c.subs {
		sub.notify()
	}
}

func (c *Computed[T]) addSource(src source) {
	c.sources[src] = struct{}{}
	src.subscribe(c)
}

func (c *Computed[T]) subscribe(o observer)   { c.subs[o] = struct{}{} }
func (c *Computed[T]) unsubscribe(o observer) { delete(c.subs, o) }

// Effect is a side-effecting observer. It runs once at creation and again
// whenever any dependency changes (immediately, or at batch exit).
type Effect struct {
	scope   *Scope
	fn      func()
	sources map[source]struct{}
	queued  bool
	stopped bool
}

// NewEffect creates an Effect and runs fn immediately to establish its
// initial dependencies.
func NewEffect(scope *Scope, fn func()) *Effect {
	e := &Effect{scope: scope, sources: make(map[source]struct{})}
	e.fn = fn
	e.run()
	return e
}

func (e *Effect) run() {
	for src := range e.sources {
		src.unsubscribe(e)
	}
	clear(e.sources)
	prev := e.scope.observer
	e.scope.observer = e
	e.fn()
	e.scope.observer = prev
}

// notify runs the effect immediately, or queues it when inside a Batch.
func (e *Effect) notify() {
	if e.stopped {
		return
	}
	if e.scope.batchDepth > 0 {
		e.scope.enqueue(e)
		return
	}
	e.run()
}

func (e *Effect) addSource(src source) {
	e.sources[src] = struct{}{}
	src.subscribe(e)
}

// Stop detaches the effect from all sources; it never runs again.
func (e *Effect) Stop() {
	if e.stopped {
		return
	}
	e.stopped = true
	for src := range e.sources {
		src.unsubscribe(e)
	}
	clear(e.sources)
}
