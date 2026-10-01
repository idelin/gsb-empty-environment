package clockguard

import (
	"sync"
	"time"
)

// ClockSource 抽象两个独立的时钟域：
//   - Wall：墙钟，受人为修改、NTP 阶跃、时区调整影响，可能前后跳变；
//   - Monotonic：单调时钟，只增不减，不受系统时间调整影响。
//
// 守护器通过持续比对两者的推进步调来检测时钟撕裂。
type ClockSource interface {
	Wall() time.Time
	Monotonic() time.Duration
}

// SystemClock 是生产环境使用的真实时钟源。
//
// Go 的 time.Now() 同时携带墙钟与单调时钟读数：Monotonic() 借助
// time.Since(boot) 只读取单调分量，因此不受系统时间回拨/阶跃影响。
type SystemClock struct {
	boot time.Time
}

func NewSystemClock() *SystemClock {
	return &SystemClock{boot: time.Now()}
}

func (c *SystemClock) Wall() time.Time { return time.Now() }

func (c *SystemClock) Monotonic() time.Duration { return time.Since(c.boot) }

// FakeClock 是可编程的模拟时钟源，用于测试与演示：
// 两个时钟域可以独立推进，从而模拟"系统时间被回拨 1 小时"
// "NTP 大幅阶跃""虚拟机挂起导致单调时钟停滞"等故障场景。
type FakeClock struct {
	mu   sync.Mutex
	wall time.Time
	mono time.Duration
}

func NewFakeClock(start time.Time) *FakeClock {
	return &FakeClock{wall: start}
}

func (c *FakeClock) Wall() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.wall
}

func (c *FakeClock) Monotonic() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.mono
}

// Advance 让两个时钟域同步前进 d，模拟一切正常的流逝。
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.wall = c.wall.Add(d)
	c.mono += d
}

// AdvanceWall 只推进（或回拨，d 为负）墙钟，单调时钟不动，
// 模拟人为修改系统时间或 NTP 阶跃。
func (c *FakeClock) AdvanceWall(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.wall = c.wall.Add(d)
}

// AdvanceMonotonic 只推进单调时钟，墙钟不动，模拟墙钟被冻结。
func (c *FakeClock) AdvanceMonotonic(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.mono += d
}
