package netsec

import (
	"sync"
	"time"
)

// Clock abstracts time so tests can drive expiry with a virtual clock.
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// ManualClock is a virtual clock for tests; time only moves when
// Advance is called.
type ManualClock struct {
	mu  sync.Mutex
	now time.Time
}

func NewManualClock(start time.Time) *ManualClock {
	return &ManualClock{now: start}
}

func (m *ManualClock) Now() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.now
}

func (m *ManualClock) Advance(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.now = m.now.Add(d)
}
