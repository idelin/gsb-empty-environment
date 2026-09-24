package negcache

import (
	"sync"
	"time"
)

// Clock is an injectable time source. Production uses time.Now; tests use
// FakeClock for deterministic TTL/backoff/expiry checks.
type Clock func() time.Time

// FakeClock is a manually advanced Clock for tests.
type FakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func NewFakeClock(start time.Time) *FakeClock { return &FakeClock{now: start} }

func (f *FakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// Advance moves the clock forward by d.
func (f *FakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}
