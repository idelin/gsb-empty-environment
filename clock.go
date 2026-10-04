package leasearb

import (
	"sync"
	"time"
)

// Clock 抽象时间来源，便于在测试中注入手动时钟、模拟时钟前跳。
type Clock interface {
	Now() time.Time
}

// SystemClock 使用真实墙钟。
type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now() }

// ManualClock 是可手动推进的时钟，用于确定性重放 / 测试。
type ManualClock struct {
	mu  sync.Mutex
	now time.Time
}

func NewManualClock(start time.Time) *ManualClock {
	return &ManualClock{now: start}
}

func (c *ManualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance 将时钟向前推进 d，调用者可以借此模拟时钟前跳、让租约"瞬间"过期。
func (c *ManualClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func (c *ManualClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}
