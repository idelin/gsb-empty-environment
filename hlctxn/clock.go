package hlctxn

import (
	"sync"
	"time"
)

// Timestamp 是混合逻辑时钟（HLC）时间戳：物理墙钟部分 WallTime（纳秒）
// 加逻辑计数器 Logical。同一物理时刻的多次滴答通过 Logical 区分全序。
type Timestamp struct {
	WallTime int64
	Logical  int32
}

// Compare 返回 -1 / 0 / 1，先比物理部分再比逻辑部分。
func (t Timestamp) Compare(o Timestamp) int {
	if t.WallTime != o.WallTime {
		if t.WallTime < o.WallTime {
			return -1
		}
		return 1
	}
	if t.Logical != o.Logical {
		if t.Logical < o.Logical {
			return -1
		}
		return 1
	}
	return 0
}

func (t Timestamp) Less(o Timestamp) bool    { return t.Compare(o) < 0 }
func (t Timestamp) LessEq(o Timestamp) bool  { return t.Compare(o) <= 0 }
func (t Timestamp) Greater(o Timestamp) bool { return t.Compare(o) > 0 }

// Next 返回同一墙钟时刻的下一个逻辑时间戳。
func (t Timestamp) Next() Timestamp {
	return Timestamp{WallTime: t.WallTime, Logical: t.Logical + 1}
}

// Add 在墙钟部分叠加 nanos 偏移（逻辑部分保持不变），用于计算
// maxTS = readTS + maxOffset。maxTS 在事务生命周期内不可变。
func (t Timestamp) Add(nanos int64) Timestamp {
	return Timestamp{WallTime: t.WallTime + nanos, Logical: t.Logical}
}

// PhysicalClock 抽象节点本地物理时钟，便于注入固定偏移与仿真时钟。
type PhysicalClock interface {
	Now() int64
}

// RealPhysicalClock 是真实墙钟叠加固定偏移（可负），模拟节点间时钟偏斜。
type RealPhysicalClock struct {
	Offset time.Duration
}

func (c RealPhysicalClock) Now() int64 {
	return time.Now().UnixNano() + c.Offset.Nanoseconds()
}

// SimPhysicalClock 是手动推进的确定性仿真时钟，充当无偏斜的“真实时间”基准。
type SimPhysicalClock struct {
	mu  sync.Mutex
	now int64
}

func NewSimPhysicalClock(start int64) *SimPhysicalClock {
	return &SimPhysicalClock{now: start}
}

func (c *SimPhysicalClock) Now() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance 手动推进仿真时间；d 为负则模拟时钟回拨，HLC 依靠逻辑计数保持单调。
func (c *SimPhysicalClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now += d.Nanoseconds()
}

// SkewedPhysicalClock 在基准时钟上叠加固定节点偏移。
type SkewedPhysicalClock struct {
	Base   PhysicalClock
	Offset time.Duration
}

func (c SkewedPhysicalClock) Now() int64 {
	return c.Base.Now() + c.Offset.Nanoseconds()
}

// Clock 实现 CockroachDB 风格 HLC：
//   - Now：物理钟回退/未前进时递增 Logical，保证时间戳全局单调；
//   - Update：接收到远端时间戳后做因果合并，保证 happened-before 顺序。
type Clock struct {
	mu      sync.Mutex
	wall    int64
	logical int32
	phys    PhysicalClock
}

func NewClock(phys PhysicalClock) *Clock {
	return &Clock{phys: phys}
}

// Now 取本地 HLC 滴答。
func (c *Clock) Now() Timestamp {
	c.mu.Lock()
	defer c.mu.Unlock()
	p := c.phys.Now()
	if p > c.wall {
		c.wall, c.logical = p, 0
	} else {
		c.logical++
	}
	return Timestamp{WallTime: c.wall, Logical: c.logical}
}

// Update 合并远端时间戳（RPC 响应、Raft apply、意图解析等因果事件），
// 返回合并后的当前 HLC 时间戳。
func (c *Clock) Update(remote Timestamp) Timestamp {
	c.mu.Lock()
	defer c.mu.Unlock()
	p := c.phys.Now()
	switch {
	case p > c.wall && p > remote.WallTime:
		c.wall, c.logical = p, 0
	case c.wall > remote.WallTime:
		c.logical++
	case remote.WallTime > c.wall:
		c.wall, c.logical = remote.WallTime, remote.Logical+1
	default:
		if remote.Logical > c.logical {
			c.logical = remote.Logical
		}
		c.logical++
	}
	return Timestamp{WallTime: c.wall, Logical: c.logical}
}

// Peek 返回当前 HLC 时间戳而不产生滴答（诊断/断言用）。
func (c *Clock) Peek() Timestamp {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Timestamp{WallTime: c.wall, Logical: c.logical}
}
