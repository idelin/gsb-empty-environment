package hlc

import (
	"sync"
	"time"
)

// PhysicalClock reads wall-clock milliseconds and waits for the clock to
// reach a target. Real and fake implementations exist so tests can
// deterministically reproduce counter overflow and same-instant concurrency.
type PhysicalClock interface {
	// NowMillis returns the current physical time in signed milliseconds.
	NowMillis() int64
	// WaitUntil blocks until NowMillis would return at least target.
	WaitUntil(target int64)
}

// SystemClock reads the real wall clock via time.Now and sleeps on overflow.
type SystemClock struct{}

// NewSystemClock returns a PhysicalClock backed by the operating system.
func NewSystemClock() SystemClock { return SystemClock{} }

// NowMillis implements PhysicalClock.
func (SystemClock) NowMillis() int64 { return time.Now().UnixMilli() }

// WaitUntil implements PhysicalClock, sleeping until the target millisecond.
func (SystemClock) WaitUntil(target int64) {
	for {
		now := time.Now().UnixMilli()
		if now >= target {
			return
		}
		sleep := time.Duration(target-now) * time.Millisecond
		time.Sleep(sleep)
	}
}

// FakeClock is a manually controlled PhysicalClock. Goroutines blocked in
// WaitUntil park on a condition and are woken whenever the time changes;
// they re-check and re-park automatically. Set allows simulating physical
// clock rollback, which the HLC must tolerate without going backwards.
type FakeClock struct {
	mu      sync.Mutex
	cond    *sync.Cond
	now     int64
	waiters int // goroutines currently blocked in WaitUntil
}

// NewFakeClock returns a fake clock initially at millisecond start.
func NewFakeClock(start int64) *FakeClock {
	fc := &FakeClock{now: start}
	fc.cond = sync.NewCond(&fc.mu)
	return fc
}

// NowMillis implements PhysicalClock.
func (f *FakeClock) NowMillis() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// WaitUntil implements PhysicalClock without real sleeping.
func (f *FakeClock) WaitUntil(target int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for f.now < target {
		f.waiters++
		f.cond.Wait()
		f.waiters--
	}
}

// HasWaiter reports whether at least one goroutine is suspended in WaitUntil.
func (f *FakeClock) HasWaiter() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.waiters > 0
}

// Set moves the clock to millis (forwards or backwards) and wakes waiters.
func (f *FakeClock) Set(millis int64) {
	f.mu.Lock()
	f.now = millis
	f.mu.Unlock()
	f.cond.Broadcast()
}

// Advance moves the clock forward by d milliseconds and wakes waiters.
func (f *FakeClock) Advance(d int64) {
	if d <= 0 {
		return
	}
	f.mu.Lock()
	f.now += d
	f.mu.Unlock()
	f.cond.Broadcast()
}
