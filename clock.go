package tasksched

import (
	"sync"
	"time"
)

// Clock abstracts time so tests can inject a manual clock.
type Clock interface {
	Now() time.Time
	NewTimer(d time.Duration) Timer
}

// Timer is a single-use countdown that fires on C.
type Timer interface {
	C() <-chan time.Time
	Stop() bool
}

// RealClock backs Clock with the wall clock.
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }

func (RealClock) NewTimer(d time.Duration) Timer { return realTimer{t: time.NewTimer(d)} }

type realTimer struct{ t *time.Timer }

func (r realTimer) C() <-chan time.Time { return r.t.C }
func (r realTimer) Stop() bool          { return r.t.Stop() }

// ManualClock is a test clock that only advances when Advance is called.
type ManualClock struct {
	mu     sync.Mutex
	now    time.Time
	nextID int
	timers map[int]*manualTimer
}

func NewManualClock(start time.Time) *ManualClock {
	return &ManualClock{now: start, timers: make(map[int]*manualTimer)}
}

func (m *ManualClock) Now() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.now
}

func (m *ManualClock) NewTimer(d time.Duration) Timer {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := &manualTimer{
		clock: m,
		id:    m.nextID,
		at:    m.now.Add(d),
		c:     make(chan time.Time, 1),
	}
	m.nextID++
	m.timers[t.id] = t
	return t
}

// Advance moves the clock forward and fires every timer due by the new time.
func (m *ManualClock) Advance(d time.Duration) {
	m.mu.Lock()
	m.now = m.now.Add(d)
	var due []*manualTimer
	for id, t := range m.timers {
		if !t.at.After(m.now) {
			due = append(due, t)
			delete(m.timers, id)
		}
	}
	m.mu.Unlock()
	for _, t := range due {
		t.c <- t.at // buffered; never blocks
	}
}

// Pending reports how many timers are still waiting to fire.
func (m *ManualClock) Pending() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.timers)
}

type manualTimer struct {
	clock *ManualClock
	id    int
	at    time.Time
	c     chan time.Time
}

func (t *manualTimer) C() <-chan time.Time { return t.c }

func (t *manualTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	if _, ok := t.clock.timers[t.id]; !ok {
		return false
	}
	delete(t.clock.timers, t.id)
	return true
}
