// Package groupbreaker implements a per-group circuit breaker with
// closed/open/half-open states, a bounded number of half-open probes,
// and generation-validated probe results and expiry callbacks so that
// stale failures can never reopen a recovered generation.
package groupbreaker

import (
	"errors"
	"fmt"
	"sync"
	"time"
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

// ErrOpen is returned (wrapped) when the circuit is open and the call is
// rejected without running the task. Use errors.Is(err, ErrOpen).
var ErrOpen = errors.New("groupbreaker: circuit is open")

// ErrProbeLimit is returned (wrapped) when the circuit is half-open and the
// probe quota is already exhausted by in-flight probes.
var ErrProbeLimit = errors.New("groupbreaker: half-open probe limit reached")

// Options configures a Breaker. All fields are injectable by tests.
type Options struct {
	// FailureThreshold is the number of consecutive failures in the closed
	// state that trips the circuit open. Defaults to 1.
	FailureThreshold int
	// OpenTimeout is how long the circuit stays open before an expiry
	// callback moves it to half-open. Defaults to 30s.
	OpenTimeout time.Duration
	// HalfOpenMaxProbes caps concurrent probes while half-open. Defaults to 1.
	HalfOpenMaxProbes int
	// IsFailure classifies a task error; nil means any non-nil error counts.
	IsFailure func(error) bool
	// Clock supplies time and the open-state expiry callback.
	// Nil means RealClock.
	Clock Clock
}

func (o *Options) withDefaults() {
	if o.FailureThreshold <= 0 {
		o.FailureThreshold = 1
	}
	if o.OpenTimeout <= 0 {
		o.OpenTimeout = 30 * time.Second
	}
	if o.HalfOpenMaxProbes <= 0 {
		o.HalfOpenMaxProbes = 1
	}
	if o.IsFailure == nil {
		o.IsFailure = func(err error) bool { return err != nil }
	}
	if o.Clock == nil {
		o.Clock = RealClock{}
	}
}

// group holds the mutable state of one named circuit.
type group struct {
	state    State
	gen      uint64 // bumped on every state transition
	failures int    // consecutive failures while closed
	probes   int    // in-flight half-open probes
	timer    Timer  // pending open-state expiry callback, if any
}

// Breaker keeps one independent circuit per group name.
type Breaker struct {
	opts Options

	mu     sync.Mutex
	groups map[string]*group
}

// New creates a Breaker. Groups are created lazily on first use.
func New(opts Options) *Breaker {
	opts.withDefaults()
	return &Breaker{opts: opts, groups: make(map[string]*group)}
}

// State reports the current state of a group (Closed for unknown groups).
func (b *Breaker) State(name string) State {
	b.mu.Lock()
	defer b.mu.Unlock()
	if g, ok := b.groups[name]; ok {
		return g.state
	}
	return Closed
}

// Do runs fn under the circuit of the named group.
//
// Open circuits reject immediately with an error matching ErrOpen.
// Half-open circuits admit at most HalfOpenMaxProbes concurrent probes;
// excess calls fail fast with an error matching ErrProbeLimit.
func (b *Breaker) Do(name string, fn func() error) error {
	g, probeGen, err := b.admit(name)
	if err != nil {
		return err
	}
	runErr := fn()
	b.complete(name, g, probeGen, runErr)
	return runErr
}

// admit decides whether a call may proceed. probeGen is non-zero when the
// call was admitted as a half-open probe of that generation.
func (b *Breaker) admit(name string) (*group, uint64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	g, ok := b.groups[name]
	if !ok {
		g = &group{state: Closed, gen: 1}
		b.groups[name] = g
	}

	switch g.state {
	case Open:
		return nil, 0, fmt.Errorf("group %q: %w", name, ErrOpen)
	case HalfOpen:
		if g.probes >= b.opts.HalfOpenMaxProbes {
			return nil, 0, fmt.Errorf("group %q: %w", name, ErrProbeLimit)
		}
		g.probes++
		return g, g.gen, nil
	default:
		return g, 0, nil
	}
}

// complete records the outcome of an admitted call.
func (b *Breaker) complete(name string, g *group, probeGen uint64, err error) {
	failure := err != nil && b.opts.IsFailure(err)

	b.mu.Lock()
	defer b.mu.Unlock()

	if probeGen != 0 {
		if probeGen != g.gen {
			// Stale probe from an old generation: its result must not
			// affect the current generation, including its probe quota.
			return
		}
		// Half-open probe of the current generation finished: free its slot.
		g.probes--
		if failure {
			b.reopenLocked(name, g)
		} else {
			b.closeLocked(g)
		}
		return
	}

	// Closed-state call.
	if g.state != Closed {
		return
	}
	if failure {
		g.failures++
		if g.failures >= b.opts.FailureThreshold {
			b.tripLocked(name, g)
		}
	} else {
		g.failures = 0
	}
}

// tripLocked moves a group from closed to open and arms the expiry callback.
func (b *Breaker) tripLocked(name string, g *group) {
	g.state = Open
	g.gen++
	g.failures = 0
	g.probes = 0
	gen := g.gen
	g.timer = b.opts.Clock.AfterFunc(b.opts.OpenTimeout, func() {
		b.expireLocked(name, gen)
	})
}

// reopenLocked moves a half-open group back to open after a failed probe.
func (b *Breaker) reopenLocked(name string, g *group) {
	g.state = Open
	g.gen++
	g.probes = 0
	gen := g.gen
	g.timer = b.opts.Clock.AfterFunc(b.opts.OpenTimeout, func() {
		b.expireLocked(name, gen)
	})
}

// closeLocked moves a group to closed after a successful probe.
func (b *Breaker) closeLocked(g *group) {
	g.state = Closed
	g.gen++
	g.failures = 0
	g.probes = 0
	if g.timer != nil {
		g.timer.Stop()
		g.timer = nil
	}
}

// expireLocked is the open-state expiry callback. The generation check
// guarantees a stale callback cannot mutate a newer generation.
func (b *Breaker) expireLocked(name string, gen uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	g, ok := b.groups[name]
	if !ok || g.state != Open || g.gen != gen {
		return
	}
	g.state = HalfOpen
	g.gen++
	g.timer = nil
}
