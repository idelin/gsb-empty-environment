package configlease

import (
	"sync"
	"time"
)

// Clock abstracts time so tests can advance it manually.
type Clock interface {
	Now() time.Time
}

// RealClock is the wall clock.
type RealClock struct{}

// Now returns the current wall-clock time.
func (RealClock) Now() time.Time { return time.Now() }

// ManualClock is a test clock advanced explicitly by the caller.
type ManualClock struct {
	mu sync.Mutex
	t  time.Time
}

// NewManualClock starts a manual clock at t.
func NewManualClock(t time.Time) *ManualClock { return &ManualClock{t: t} }

// Now returns the manual clock's current time.
func (m *ManualClock) Now() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.t
}

// Advance moves the manual clock forward by d.
func (m *ManualClock) Advance(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.t = m.t.Add(d)
}
