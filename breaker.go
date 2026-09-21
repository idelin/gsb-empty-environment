// Package breaker implements a per-group circuit breaker with closed, open
// and half-open states. Half-open probes are quota-limited, and both probe
// results and open-state expiry callbacks are validated against a generation
// counter so that stale events from an old generation can never reopen or
// otherwise mutate a newer generation that has already recovered.
//
// Time and scheduling are injected via the Clock and Scheduler interfaces,
// and the guarded work is supplied by the caller as a task function, which
// makes the breaker fully deterministic under test.
package breaker

import (
	"errors"
	"sync"
	"time"
)

// Identifiable errors returned by Execute.
var (
	// ErrOpen is returned immediately when the group's circuit is open.
	ErrOpen = errors.New("breaker: circuit is open")
	// ErrProbeLimit is returned when the group is half-open and the probe
	// quota is already exhausted by in-flight probes.
	ErrProbeLimit = errors.New("breaker: half-open probe quota exhausted")
)

// State is the circuit state of one group.
type State int

const (
	Closed State = iota
	Open
	HalfOpen
)

func (s State) String() string {
	switch s {
	case Closed:
		return "closed"
	case Open:
		return "open"
	case HalfOpen:
		return "half-open"
	}
	return "unknown"
}

// Clock supplies the current time. Tests inject a fake.
type Clock interface {
	Now() time.Time
}

// Scheduler schedules the open-state expiry callback. Tests inject a fake.
type Scheduler interface {
	AfterFunc(d time.Duration, f func())
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

type realScheduler struct{}

func (realScheduler) AfterFunc(d time.Duration, f func()) { time.AfterFunc(d, f) }

// Options configures a Manager. Zero values fall back to sane defaults.
type Options struct {
	// FailureThreshold is the number of consecutive closed-state failures
	// that opens the circuit.
	FailureThreshold int
	// OpenDuration is how long the circuit stays open before the expiry
	// callback moves it to half-open.
	OpenDuration time.Duration
	// HalfOpenMaxProbes caps the number of concurrent probe requests
	// admitted while half-open.
	HalfOpenMaxProbes int
	// Clock and Scheduler default to real time when nil.
	Clock     Clock
	Scheduler Scheduler
}

func (o Options) withDefaults() Options {
	if o.FailureThreshold <= 0 {
		o.FailureThreshold = 1
	}
	if o.OpenDuration <= 0 {
		o.OpenDuration = time.Second
	}
	if o.HalfOpenMaxProbes <= 0 {
		o.HalfOpenMaxProbes = 1
	}
	if o.Clock == nil {
		o.Clock = realClock{}
	}
	if o.Scheduler == nil {
		o.Scheduler = realScheduler{}
	}
	return o
}

// Manager keeps one independent circuit per group name.
type Manager struct {
	opts Options

	mu     sync.Mutex
	groups map[string]*group
}

// NewManager returns a Manager with per-group circuits.
func NewManager(opts Options) *Manager {
	return &Manager{opts: opts.withDefaults(), groups: make(map[string]*group)}
}

// State reports the current circuit state of a group. Unknown groups are
// reported as Closed.
func (m *Manager) State(name string) State {
	m.mu.Lock()
	g, ok := m.groups[name]
	m.mu.Unlock()
	if !ok {
		return Closed
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.state
}

// Execute runs task under the protection of the named group's circuit.
//
//   - Open: returns ErrOpen immediately without running task.
//   - Half-open: admits up to HalfOpenMaxProbes concurrent probes; further
//     requests fail fast with ErrProbeLimit.
//   - Closed: runs task and counts consecutive failures.
func (m *Manager) Execute(name string, task func() error) error {
	g := m.group(name)

	g.mu.Lock()
	switch g.state {
	case Open:
		g.mu.Unlock()
		return ErrOpen
	case HalfOpen:
		if g.inflight >= g.opts.HalfOpenMaxProbes {
			g.mu.Unlock()
			return ErrProbeLimit
		}
		g.inflight++
		gen := g.generation
		g.mu.Unlock()

		err := task()
		g.finishProbe(gen, err)
		return err
	}
	g.mu.Unlock()

	err := task()
	g.recordClosed(err)
	return err
}

func (m *Manager) group(name string) *group {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.groups[name]
	if !ok {
		g = &group{opts: m.opts, state: Closed}
		m.groups[name] = g
	}
	return g
}

// group is the circuit of a single group. generation is bumped on every
// state transition; probe results and expiry callbacks carry the generation
// they belong to and are discarded when it no longer matches.
type group struct {
	opts Options

	mu         sync.Mutex
	state      State
	generation uint64
	failures   int // consecutive failures while closed
	inflight   int // in-flight half-open probes
}

func (g *group) recordClosed(err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state != Closed {
		return // state moved on while the task was running
	}
	if err == nil {
		g.failures = 0
		return
	}
	g.failures++
	if g.failures >= g.opts.FailureThreshold {
		g.openLocked()
	}
}

func (g *group) finishProbe(gen uint64, err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.generation != gen {
		return // stale probe from an old generation: must not affect the new one
	}
	g.inflight--
	if err != nil {
		g.openLocked()
		return
	}
	g.state = Closed
	g.generation++
	g.failures = 0
	g.inflight = 0
}

func (g *group) openLocked() {
	g.state = Open
	g.generation++
	g.failures = 0
	g.inflight = 0
	gen := g.generation
	g.opts.Scheduler.AfterFunc(g.opts.OpenDuration, func() { g.expire(gen) })
}

func (g *group) expire(gen uint64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state != Open || g.generation != gen {
		return // stale expiry callback from an old generation
	}
	g.state = HalfOpen
	g.generation++
	g.inflight = 0
}
