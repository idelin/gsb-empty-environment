// Package sim is a deterministic, discrete-time, event-driven
// simulation engine with execution recording, replay and fault
// injection for bug reproduction.
//
// Determinism contract:
//   - events are executed in (Time, Seq) order; Seq is assigned
//     monotonically at schedule time, so equal-time events run in
//     schedule order;
//   - the only randomness source is the engine's seeded RNG;
//   - every executed event folds its metadata into a running FNV-1a
//     hash, so two runs can be compared bit-for-bit via their Records.
package sim

import (
	"container/heap"
	"fmt"
	"sync"
)

// Event is a unit of simulation work.
type Event struct {
	Time uint64 // absolute tick at which the event fires
	Seq  uint64 // schedule-order tie breaker, assigned by the engine
	Kind string // handler routing key
	Data any    // opaque payload
}

// Handler processes one event. The engine pointer is valid only for
// the duration of the call.
type Handler func(e *Engine, ev Event)

// FaultFunc rewrites an event at schedule time. Returning ok=false
// drops the event. Faults are applied in registration order and are
// part of the deterministic configuration: the same seed + faults +
// initial workload always produces the same execution.
type FaultFunc func(ev Event) (out Event, ok bool)

// Step is one recorded event execution.
type Step struct {
	Time uint64
	Seq  uint64
	Kind string
	Hash uint64 // running hash after this step
}

// Record is the full execution trace of a run.
type Record struct {
	Seed  int64
	Steps []Step
}

// FinalHash returns the hash after the last executed step (the FNV
// offset basis for an empty run).
func (r *Record) FinalHash() uint64 {
	if len(r.Steps) == 0 {
		return fnvOffset64
	}
	return r.Steps[len(r.Steps)-1].Hash
}

const (
	fnvOffset64 = 14695981039346656037
	fnvPrime64  = 1099511628211
)

// Engine is a single-threaded event loop. Schedule, Uint64n and Now
// are safe for concurrent use; Run and RunSteps are not reentrant.
type Engine struct {
	mu       sync.Mutex
	now      uint64
	seq      uint64
	queue    eventHeap
	handlers map[string]Handler
	faults   []FaultFunc
	rngState uint64 // xorshift64* state, never 0

	hash     uint64
	executed uint64
	record   *Record // non-nil while recording
}

// New returns an engine whose RNG is seeded with seed (0 is mapped to
// a fixed non-zero state).
func New(seed int64) *Engine {
	state := uint64(seed)
	if state == 0 {
		state = 0x9E3779B97F4A7C15
	}
	return &Engine{
		handlers: make(map[string]Handler),
		rngState: state,
		hash:     fnvOffset64,
	}
}

// Handle registers h for events of the given kind.
func (e *Engine) Handle(kind string, h Handler) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.handlers[kind] = h
}

// AddFault appends a schedule-time fault injector.
func (e *Engine) AddFault(f FaultFunc) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.faults = append(e.faults, f)
}

// Now returns the current simulation tick.
func (e *Engine) Now() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.now
}

// Executed returns the number of events executed so far.
func (e *Engine) Executed() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.executed
}

// Pending returns the number of queued, not-yet-executed events.
func (e *Engine) Pending() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.queue)
}

// Uint64n returns a deterministic pseudo-random number in [0, n).
// It is the only sanctioned randomness source inside handlers.
func (e *Engine) Uint64n(n uint64) uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	// xorshift64*
	x := e.rngState
	x ^= x >> 12
	x ^= x << 25
	x ^= x >> 27
	e.rngState = x
	v := x * 2685821657736338717
	if n == 0 {
		return v
	}
	return v % n
}

// Observe folds a user-state value into the running hash, letting
// handlers make their state part of the regression fingerprint.
func (e *Engine) Observe(v uint64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.mix(v)
}

// Hash returns the current running hash.
func (e *Engine) Hash() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.hash
}

func (e *Engine) mix(v uint64) {
	for i := 0; i < 8; i++ {
		e.hash ^= (v >> (8 * uint(i))) & 0xff
		e.hash *= fnvPrime64
	}
}

func (e *Engine) mixString(s string) {
	for i := 0; i < len(s); i++ {
		e.hash ^= uint64(s[i])
		e.hash *= fnvPrime64
	}
}

// Schedule inserts an event delay ticks in the future. Fault
// injectors may rewrite or drop it. It returns false if the event was
// dropped by a fault.
func (e *Engine) Schedule(delay uint64, kind string, data any) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	ev := Event{Time: e.now + delay, Seq: e.seq, Kind: kind, Data: data}
	e.seq++
	for _, f := range e.faults {
		var ok bool
		ev, ok = f(ev)
		if !ok {
			return false
		}
	}
	heap.Push(&e.queue, ev)
	return true
}

// StartRecording begins capturing the execution trace.
func (e *Engine) StartRecording(seed int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.record = &Record{Seed: seed}
}

// StopRecording ends capture and returns the trace.
func (e *Engine) StopRecording() *Record {
	e.mu.Lock()
	defer e.mu.Unlock()
	r := e.record
	e.record = nil
	if r == nil {
		return &Record{}
	}
	return r
}

// RunSteps executes at most n events, stopping early when the queue
// drains. It returns the number of events executed.
func (e *Engine) RunSteps(n uint64) uint64 {
	var ran uint64
	for ran < n {
		e.mu.Lock()
		if len(e.queue) == 0 {
			e.mu.Unlock()
			break
		}
		ev := heap.Pop(&e.queue).(Event)
		h := e.handlers[ev.Kind]
		e.now = ev.Time
		e.mu.Unlock()

		if h != nil {
			h(e, ev)
		}

		e.mu.Lock()
		e.mix(ev.Time)
		e.mix(ev.Seq)
		e.mixString(ev.Kind)
		e.executed++
		if e.record != nil {
			e.record.Steps = append(e.record.Steps, Step{
				Time: ev.Time, Seq: ev.Seq, Kind: ev.Kind, Hash: e.hash,
			})
		}
		ran++
		e.mu.Unlock()
	}
	return ran
}

// RunUntil executes all events scheduled at or before tick `until`.
func (e *Engine) RunUntil(until uint64) uint64 {
	var ran uint64
	for {
		e.mu.Lock()
		if len(e.queue) == 0 || e.queue[0].Time > until {
			e.mu.Unlock()
			break
		}
		e.mu.Unlock()
		ran += e.RunSteps(1)
	}
	return ran
}

// Drain executes every queued event.
func (e *Engine) Drain() uint64 {
	for {
		if e.RunSteps(1) == 0 {
			return e.Executed()
		}
	}
}

// Diff compares two execution traces and returns a description of the
// first divergence, or "" if they are identical.
func Diff(a, b *Record) string {
	n := len(a.Steps)
	if len(b.Steps) < n {
		n = len(b.Steps)
	}
	for i := 0; i < n; i++ {
		x, y := a.Steps[i], b.Steps[i]
		if x.Time != y.Time || x.Seq != y.Seq || x.Kind != y.Kind || x.Hash != y.Hash {
			return fmt.Sprintf("step %d: (%d,%d,%s,h=%016x) != (%d,%d,%s,h=%016x)",
				i, x.Time, x.Seq, x.Kind, x.Hash, y.Time, y.Seq, y.Kind, y.Hash)
		}
	}
	if len(a.Steps) != len(b.Steps) {
		return fmt.Sprintf("length: %d != %d", len(a.Steps), len(b.Steps))
	}
	return ""
}

// Replay re-executes a workload deterministically and returns its
// trace. Together with Diff it is the bug-reproduction harness: a
// recorded faulty run replays to a bit-identical trace.
func Replay(seed int64, faults []FaultFunc, init func(e *Engine), steps uint64) *Record {
	e := New(seed)
	for _, f := range faults {
		e.AddFault(f)
	}
	e.StartRecording(seed)
	init(e)
	e.RunSteps(steps)
	return e.StopRecording()
}

// DropEvery returns a fault that drops every n-th event of the given
// kind (counting only events of that kind).
func DropEvery(kind string, n int) FaultFunc {
	var count int
	return func(ev Event) (Event, bool) {
		if ev.Kind != kind {
			return ev, true
		}
		count++
		if n > 0 && count%n == 0 {
			return Event{}, false
		}
		return ev, true
	}
}

// DelayKind returns a fault that postpones every event of the given
// kind by extra ticks.
func DelayKind(kind string, extra uint64) FaultFunc {
	return func(ev Event) (Event, bool) {
		if ev.Kind == kind {
			ev.Time += extra
		}
		return ev, true
	}
}

// eventHeap is a min-heap ordered by (Time, Seq).
type eventHeap []Event

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	if h[i].Time != h[j].Time {
		return h[i].Time < h[j].Time
	}
	return h[i].Seq < h[j].Seq
}
func (h eventHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *eventHeap) Push(x any)   { *h = append(*h, x.(Event)) }
func (h *eventHeap) Pop() any {
	old := *h
	n := len(old)
	ev := old[n-1]
	old[n-1] = Event{}
	*h = old[:n-1]
	return ev
}
