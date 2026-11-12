package locktable

import (
	"sync"
	"time"
)

// Clock 抽象锁表使用的时间源。生产环境使用 RealClock；测试注入
// FakeClock 即可确定性地驱动宽限期过期，无需真实等待 10 秒。
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

// RealClock 基于标准库 time 的真实时钟。
type RealClock struct{}

// NewRealClock 返回真实时钟单例。
func NewRealClock() RealClock { return RealClock{} }

func (RealClock) Now() time.Time { return time.Now() }

func (RealClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

type fakeTimer struct {
	deadline time.Time
	ch       chan time.Time
}

// FakeClock 是测试用的可控时钟：Advance 推进虚拟时间并同步触发到期定时器。
type FakeClock struct {
	mu       sync.Mutex
	now      time.Time
	nextID   int
	pending  map[int]*fakeTimer
	notifyCh chan struct{}
}

// NewFakeClock 以 start 为初始虚拟时间构造测试时钟。
func NewFakeClock(start time.Time) *FakeClock {
	return &FakeClock{
		now:      start,
		pending:  make(map[int]*fakeTimer),
		notifyCh: make(chan struct{}, 1),
	}
}

// Now 返回当前虚拟时间。
func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// After 注册一个在虚拟时间推进 d 后触发的定时器。
func (c *FakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	timer := &fakeTimer{
		deadline: c.now.Add(d),
		ch:       make(chan time.Time, 1),
	}
	c.nextID++
	c.pending[c.nextID] = timer
	select {
	case c.notifyCh <- struct{}{}:
	default:
	}
	return timer.ch
}

// WaitForTimers 阻塞到至少 count 个定时器已注册，供测试与后台协程同步，
// 避免 Advance 早于 After 执行的竞态。
func (c *FakeClock) WaitForTimers(count int) {
	for {
		c.mu.Lock()
		have := len(c.pending)
		ch := c.notifyCh
		c.mu.Unlock()
		if have >= count {
			return
		}
		<-ch
	}
}

// Advance 把虚拟时间推进 d，并触发所有已到期的定时器。
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	due := make([]*fakeTimer, 0)
	for id, timer := range c.pending {
		if !timer.deadline.After(c.now) {
			delete(c.pending, id)
			due = append(due, timer)
		}
	}
	firedAt := c.now
	c.mu.Unlock()

	for _, timer := range due {
		select {
		case timer.ch <- firedAt:
		default:
		}
	}
}
