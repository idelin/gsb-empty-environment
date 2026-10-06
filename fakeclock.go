package objstore

import (
	"sync"
	"time"
)

// FakeClock 是虚拟时钟：时间只随 Advance/SetNow 前进，
// 到期的定时任务按触发时间（同刻按注册顺序）同步执行。
// 执行回调时不持有时钟锁，避免与业务锁形成锁序环。
type FakeClock struct {
	mu     sync.Mutex
	now    time.Time
	seq    int
	timers []*fakeTimer
}

type fakeTimer struct {
	clock   *FakeClock
	at      time.Time
	seq     int
	f       func()
	fired   bool
	stopped bool
}

// NewFakeClock 返回一个起始时间为 start 的虚拟时钟。
func NewFakeClock(start time.Time) *FakeClock {
	return &FakeClock{now: start}
}

// Now 返回当前虚拟时间。
func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// AfterFunc 注册在虚拟时间 d 后触发的任务；任务在下一次 Advance/SetNow 时同步执行。
func (c *FakeClock) AfterFunc(d time.Duration, f func()) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{clock: c, at: c.now.Add(d), seq: c.seq, f: f}
	c.seq++
	c.timers = append(c.timers, t)
	return t
}

// Advance 将时钟前进 d，并同步触发所有到期任务（含回调执行期间新注册且已到期的任务）。
func (c *FakeClock) Advance(d time.Duration) {
	c.SetNow(c.Now().Add(d))
}

// SetNow 将时钟设置为 t（不允许回拨），并同步触发所有到期任务。
func (c *FakeClock) SetNow(t time.Time) {
	for {
		c.mu.Lock()
		if t.Before(c.now) {
			c.mu.Unlock()
			panic("objstore: fake clock cannot move backwards")
		}
		c.now = t
		next := c.nextDueLocked()
		if next == nil {
			c.compactLocked()
			c.mu.Unlock()
			return
		}
		next.fired = true
		f := next.f
		c.mu.Unlock()
		f()
	}
}

func (c *FakeClock) nextDueLocked() *fakeTimer {
	var next *fakeTimer
	for _, ft := range c.timers {
		if ft.fired || ft.stopped || ft.at.After(c.now) {
			continue
		}
		if next == nil || ft.at.Before(next.at) ||
			(ft.at.Equal(next.at) && ft.seq < next.seq) {
			next = ft
		}
	}
	return next
}

func (c *FakeClock) compactLocked() {
	kept := c.timers[:0]
	for _, ft := range c.timers {
		if !ft.fired && !ft.stopped {
			kept = append(kept, ft)
		}
	}
	c.timers = kept
}

func (t *fakeTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	if t.fired || t.stopped {
		return false
	}
	t.stopped = true
	return true
}
