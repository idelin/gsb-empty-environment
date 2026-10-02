package clockguard

import (
	"sync"
	"time"
)

// Clock 抽象双时钟源：墙钟（Wall-Clock）与单调时钟（Monotonic）。
type Clock interface {
	// Wall 返回当前墙钟时间，可能被人为修改、NTP 阶跃或虚拟机挂起影响。
	Wall() time.Time
	// Monotonic 返回自进程启动以来的单调 elapsed 时间，只增不减，不受系统时间调整影响。
	Monotonic() time.Duration
}

// RealClock 基于系统时钟的真实实现。
type RealClock struct {
	start time.Time // time.Now() 同时携带墙钟与单调读数
}

func NewRealClock() *RealClock {
	return &RealClock{start: time.Now()}
}

func (c *RealClock) Wall() time.Time { return time.Now() }

// Monotonic 利用 Go 运行时的单调时钟读数，不受 settimeofday / NTP 阶跃影响。
func (c *RealClock) Monotonic() time.Duration { return time.Since(c.start) }

// FakeClock 测试与演示用的可操控时钟，可分别操控两个时钟源。
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

// Advance 正常推进：两个时钟源同步前进 d。
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.wall = c.wall.Add(d)
	c.mono += d
}

// JumpWall 仅阶跃墙钟，模拟人为修改系统时间或 NTP 大幅校正。
func (c *FakeClock) JumpWall(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.wall = c.wall.Add(d)
}

// AdvanceMonotonicOnly 仅推进单调时钟，模拟墙钟挂起（如虚拟机时钟冻结）。
func (c *FakeClock) AdvanceMonotonicOnly(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.mono += d
}
