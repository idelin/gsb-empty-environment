package main

// Clock 显式时钟模型: 读数 = 真实虚拟时间 * drift + offset。
// drift != 1 表示走速偏差, offset != 0 表示相位偏差。
// 进程暂停(GC stop-the-world 等)通过 Freeze 显式建模为时钟静止。
// 租约的过期判断发生在各自的本地时钟域上, 偏差由此进入系统,
// 这正是"租约不能当作强隔离"的原因 —— 真正的隔离由 fencing token 提供。
type Clock struct {
	sim    *Sim
	offset int64
	drift  float64
}

func NewClock(sim *Sim, offset int64, drift float64) *Clock {
	return &Clock{sim: sim, offset: offset, drift: drift}
}

// Now 返回本地时钟读数(ms)。
func (c *Clock) Now() int64 {
	return int64(float64(c.sim.now)*c.drift) + c.offset
}

// Freeze 建模进程暂停 d 毫秒期间本地时钟完全静止。
func (c *Clock) Freeze(d int64) { c.offset -= d }
