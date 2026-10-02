// Package sim is a deterministic discrete-time event-driven simulation
// framework. Events are executed in a total order of (time, priority,
// sequence), a seeded splitmix64 generator supplies all randomness, and
// a trace recorder plus replayer support million-step regression tests
// and exact bug reproduction.
package sim

import (
	"container/heap"
	"sync"
)

// EventFunc runs when its event fires. It may schedule further events.
type EventFunc func(e *Engine)

type event struct {
	time uint64
	prio int
	seq  uint64
	tag  string
	fn   EventFunc
}

// eventHeap is a min-heap ordered by (time, prio, seq): fully
// deterministic even when many events share a timestamp.
type eventHeap []event

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	a, b := h[i], h[j]
	if a.time != b.time {
		return a.time < b.time
	}
	if a.prio != b.prio {
		return a.prio < b.prio
	}
	return a.seq < b.seq
}
func (h eventHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *eventHeap) Push(x any)   { *h = append(*h, x.(event)) }
func (h *eventHeap) Pop() any {
	old := *h
	n := len(old)
	ev := old[n-1]
	old[n-1] = event{} // drop the closure reference
	*h = old[:n-1]
	return ev
}

// RNG is a splitmix64 generator: tiny, fast, and stable across Go
// versions, which keeps regression hashes reproducible.
type RNG struct {
	state uint64
}

// NewRNG returns a generator seeded with seed.
func NewRNG(seed uint64) *RNG { return &RNG{state: seed} }

// Next returns the next 64-bit value.
func (r *RNG) Next() uint64 {
	r.state += 0x9E3779B97F4A7C15
	z := r.state
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// Intn returns a value in [0, n). n must be positive.
func (r *RNG) Intn(n int) int { return int(r.Next() % uint64(n)) }

// Engine schedules and executes events in deterministic order.
type Engine struct {
	mu      sync.Mutex
	h       eventHeap
	now     uint64
	seq     uint64
	step    uint64
	stopped bool
	rng     *RNG
}

// New returns an engine whose random stream is seeded with seed.
func New(seed uint64) *Engine {
	return &Engine{rng: NewRNG(seed)}
}

// Now is the simulation time of the last executed event.
func (e *Engine) Now() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.now
}

// Step is the number of events executed so far.
func (e *Engine) Step() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.step
}

// NextUint64 draws from the engine's deterministic random stream.
// Call it only from event handlers, which run single-threaded.
func (e *Engine) NextUint64() uint64 { return e.rng.Next() }

// Intn draws a value in [0, n) from the deterministic stream.
func (e *Engine) Intn(n int) int { return e.rng.Intn(n) }

// Schedule inserts an event delay ticks after the current time. It is
// safe to call from any goroutine, including handlers.
func (e *Engine) Schedule(delay uint64, prio int, tag string, fn EventFunc) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.seq++
	heap.Push(&e.h, event{time: e.now + delay, prio: prio, seq: e.seq, tag: tag, fn: fn})
}

// Stop halts the run loop after the current event.
func (e *Engine) Stop() {
	e.mu.Lock()
	e.stopped = true
	e.mu.Unlock()
}

// StepOnce executes the next event, returning false when the queue is
// empty or the engine was stopped.
func (e *Engine) StepOnce() bool {
	e.mu.Lock()
	if e.stopped || len(e.h) == 0 {
		e.mu.Unlock()
		return false
	}
	ev := heap.Pop(&e.h).(event)
	e.now = ev.time
	e.step++
	e.mu.Unlock()
	ev.fn(e)
	return true
}

// RunSteps executes up to max events.
func (e *Engine) RunSteps(max uint64) {
	for i := uint64(0); i < max; i++ {
		if !e.StepOnce() {
			return
		}
	}
}

// Run executes events with time <= until.
func (e *Engine) Run(until uint64) {
	for {
		e.mu.Lock()
		empty := e.stopped || len(e.h) == 0 || e.h[0].time > until
		e.mu.Unlock()
		if empty || !e.StepOnce() {
			return
		}
	}
}
