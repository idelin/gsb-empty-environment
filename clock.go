package netsec

import "sync"

// Clock 是控制宿主读取时间的抽象。生产环境可使用 SystemClock，
// 测试中使用 VirtualClock 以确定性方式推进时间。
type Clock interface {
	Now() Time
}

// Time 使用单调递增的逻辑时间戳（例如 Unix 纳秒），
// 避免测试依赖墙上时钟。
type Time int64

// SystemClock 读取真实墙上时间。
type SystemClock struct{}

// Now 返回当前逻辑时间。
func (SystemClock) Now() Time {
	return nowUnixNano()
}

// VirtualClock 是可手动推进的确定性时钟。
type VirtualClock struct {
	mu  sync.Mutex
	now Time
}

// NewVirtualClock 以起点 start 创建虚拟时钟。
func NewVirtualClock(start Time) *VirtualClock {
	return &VirtualClock{now: start}
}

// Now 返回当前虚拟时间。
func (c *VirtualClock) Now() Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance 将虚拟时钟向前推进 d 个时间单位，返回推进后的时间。
func (c *VirtualClock) Advance(d Time) Time {
	if d < 0 {
		d = 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now += d
	return c.now
}

// Set 直接把虚拟时钟设置为 t；早于当前时间的设置会被忽略。
func (c *VirtualClock) Set(t Time) Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	if t > c.now {
		c.now = t
	}
	return c.now
}
