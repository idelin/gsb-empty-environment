// Package coalesce provides an invalidation-driven refresh coalescer.
//
// The package is intentionally built only on the Go standard library. Clock,
// data source loading and error reporting are injected so the behaviour is
// fully deterministic in tests (see ManualClock).
package coalesce

import (
	"sync"
	"time"
)

// Clock is the injectable time source. RealClock drives production with
// wall-clock time; ManualClock drives deterministic tests.
type Clock interface {
	// Now returns the current time.
	Now() time.Time
	// NewTicker returns a Ticker that fires on C.
	NewTicker(d time.Duration) Ticker
}

// Ticker mirrors time.Ticker so it can be replaced in tests.
type Ticker interface {
	C() <-chan time.Time
	Stop()
}

// RealClock is the production Clock backed by the standard library.
type RealClock struct{}

// NewRealClock returns a Clock backed by the wall clock.
func NewRealClock() RealClock { return RealClock{} }

func (RealClock) Now() time.Time { return time.Now() }

func (RealClock) NewTicker(d time.Duration) Ticker {
	if d <= 0 {
		d = time.Nanosecond
	}
	return &realTicker{t: time.NewTicker(d)}
}

type realTicker struct{ t *time.Ticker }

func (r *realTicker) C() <-chan time.Time { return r.t.C }
func (r *realTicker) Stop()               { r.t.Stop() }

// ManualClock is a deterministic Clock for tests and the sample program.
// Advance fires every pending tick exactly once (and coalesces further
// advances while nobody has drained the channel, exactly like time.Ticker).
type ManualClock struct {
	mu    sync.Mutex
	now   time.Time
	ticks []*manualTicker
}

// NewManualClock returns a manual clock starting at the Unix epoch.
func NewManualClock() *ManualClock {
	return &ManualClock{now: time.Unix(0, 0)}
}

func (m *ManualClock) Now() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.now
}

func (m *ManualClock) NewTicker(time.Duration) Ticker {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := &manualTicker{ch: make(chan time.Time, 1), clock: m, live: true}
	m.ticks = append(m.ticks, t)
	return t
}

// Advance moves the clock forward and fires every live ticker once.
func (m *ManualClock) Advance(d time.Duration) {
	m.mu.Lock()
	m.now = m.now.Add(d)
	ticks := append([]*manualTicker(nil), m.ticks...)
	m.mu.Unlock()
	for _, t := range ticks {
		t.fire(m.Now())
	}
}

type manualTicker struct {
	ch    chan time.Time
	clock *ManualClock
	mu    sync.Mutex
	live  bool
}

func (t *manualTicker) C() <-chan time.Time { return t.ch }

func (t *manualTicker) fire(now time.Time) {
	t.mu.Lock()
	live := t.live
	t.mu.Unlock()
	if !live {
		return
	}
	select {
	case t.ch <- now:
	default: // consumer is behind: coalesce, like a real 1-capacity ticker.
	}
}

func (t *manualTicker) Stop() {
	t.mu.Lock()
	t.live = false
	t.mu.Unlock()

	t.clock.mu.Lock()
	for i, x := range t.clock.ticks {
		if x == t {
			t.clock.ticks = append(t.clock.ticks[:i], t.clock.ticks[i+1:]...)
			break
		}
	}
	t.clock.mu.Unlock()
}
