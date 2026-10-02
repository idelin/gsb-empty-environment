package lease

import (
	"sync"
	"time"
)

// Clock 抽象时钟：Wall 为墙上时间（可被 NTP 校时/跳变影响），
// Mono 为单调递增的进程内时间（不受校时影响）。
// 安全判定同时参考两者，差异超过最大漂移即视为时钟异常。
type Clock interface {
	Wall() time.Time
	Mono() time.Duration
}

// RealClock 真实时钟。
type RealClock struct {
	start time.Time
}

func NewRealClock() *RealClock { return &RealClock{start: time.Now()} }

func (c *RealClock) Wall() time.Time     { return time.Now() }
func (c *RealClock) Mono() time.Duration { return time.Since(c.start) }

// ManualClock 可手动推进/注入跳变的时钟，用于测试与故障注入。
type ManualClock struct {
	mu   sync.Mutex
	wall time.Time
	mono time.Duration
}

func NewManualClock(start time.Time) *ManualClock {
	return &ManualClock{wall: start}
}

func (c *ManualClock) Wall() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.wall
}

func (c *ManualClock) Mono() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.mono
}

// Advance 正常推进：墙上时间与单调时间同步前进。
func (c *ManualClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.wall = c.wall.Add(d)
	c.mono += d
}

// JumpWall 仅拨动墙上时间（模拟 NTP 校时突变/时钟前拨），单调时间不变。
func (c *ManualClock) JumpWall(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.wall = c.wall.Add(d)
}
