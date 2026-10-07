package msgqueue

import (
	"sync"
	"time"
)

// Clock abstracts time so tests can drive leases deterministically.
type Clock interface {
	Now() time.Time
}

// VirtualClock is a manually advanced Clock for tests.
type VirtualClock struct {
	mu  sync.Mutex
	now time.Time
}

func NewVirtualClock(start time.Time) *VirtualClock {
	return &VirtualClock{now: start}
}

func (v *VirtualClock) Now() time.Time {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.now
}

func (v *VirtualClock) Advance(d time.Duration) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.now = v.now.Add(d)
}
