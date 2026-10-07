package ttlcache

import (
	"sync"
	"time"
)

// Clock 显式提供单调时间，单位为自某一任意起点起的时长。
// 缓存内部绝不直接调用 time.Now，所有时间判断都经由 Clock，
// 因此挂钟回拨不会影响过期语义。
type Clock interface {
	Now() time.Duration
}

// SystemClock 基于进程启动时刻的单调时钟（time.Since 只读取
// 单调分量，不受挂钟调整影响）。
type SystemClock struct {
	epoch time.Time
}

func NewSystemClock() *SystemClock {
	return &SystemClock{epoch: time.Now()}
}

func (c *SystemClock) Now() time.Duration {
	return time.Since(c.epoch)
}

// ManualClock 测试用手动时钟，只能通过 Advance 前进。
type ManualClock struct {
	mu  sync.Mutex
	now time.Duration
}

func NewManualClock() *ManualClock {
	return &ManualClock{}
}

func (c *ManualClock) Now() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *ManualClock) Advance(d time.Duration) {
	if d < 0 {
		panic("ManualClock: cannot move backwards")
	}
	c.mu.Lock()
	c.now += d
	c.mu.Unlock()
}
