package merge

import "sync/atomic"

// LamportClock 是 Lamport 逻辑时钟，用于跨分区时钟校正。
// 规则：
//   - 本地事件发生时 Tick()，时钟 +1；
//   - 收到携带对端时钟的消息时 Witness(other)，
//     时钟校正为 max(local, other) + 1。
type LamportClock struct {
	v atomic.Uint64
}

// Tick 推进本地时钟并返回新值。
func (c *LamportClock) Tick() uint64 {
	return c.v.Add(1)
}

// Witness 观测到对端时钟，将本地时钟校正为 max(local, other)+1。
func (c *LamportClock) Witness(other uint64) uint64 {
	for {
		cur := c.v.Load()
		next := cur + 1
		if other >= next {
			next = other + 1
		}
		if c.v.CompareAndSwap(cur, next) {
			return next
		}
	}
}

// Value 返回当前时钟值。
func (c *LamportClock) Value() uint64 {
	return c.v.Load()
}
