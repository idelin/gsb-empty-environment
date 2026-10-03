package swim

import "time"

// Clock 抽象时间来源，测试中使用 ManualClock 实现确定性推进。
type Clock interface {
	Now() time.Time
	Advance(d time.Duration)
}

// ManualClock 是测试时钟，时间只由显式推进（如模拟网络延迟）。
type ManualClock struct {
	now time.Time
}

func NewManualClock() *ManualClock {
	return &ManualClock{now: time.Unix(0, 0)}
}

func (c *ManualClock) Now() time.Time { return c.now }

func (c *ManualClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

// RealClock 使用真实墙钟，供演示程序使用。
type RealClock struct{}

func (RealClock) Now() time.Time          { return time.Now() }
func (RealClock) Advance(d time.Duration) {}
