// Package netem 提供确定性的帧驱动网络模拟器与对战中继。
//
// 模拟器不使用 goroutine 或系统时钟，全部由离散 tick 驱动，
// 随机延迟与丢包由可播种的 PCG 生成，因此测试可以完全复现。
package netem

import (
	"math/rand/v2"

	"example.org/rollback/netcode"
)

// inFlight 是一条正在链路上传输的消息。
type inFlight struct {
	bundle    netcode.Bundle
	deliverAt int
}

// Emulator 模拟一条带随机延迟与丢包的单向/双向链路。
type Emulator struct {
	rng        *rand.Rand
	minLatency int // 最小延迟（tick 数）
	jitter     int // 额外均匀抖动范围 [0, jitter]
	lossRate   float64
	tick       int
	queue      []inFlight
	lost       int
	delivered  int
	blocked    bool // 为 true 时消息继续滞留、不投递，模拟链路中断
}

// NewEmulator 创建模拟器。minLatency 为固定基础延迟，jitter 为随机抖动
// 上限，lossRate 为 [0,1) 的单包丢弃概率，seed 决定随机序列。
func NewEmulator(minLatency, jitter int, lossRate float64, seed uint64) *Emulator {
	if minLatency < 0 {
		minLatency = 0
	}
	if jitter < 0 {
		jitter = 0
	}
	return &Emulator{
		rng:        rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)),
		minLatency: minLatency,
		jitter:     jitter,
		lossRate:   lossRate,
	}
}

// Send 在当前 tick 注入一个消息包；按丢包率随机丢弃，否则安排延迟投递。
func (e *Emulator) Send(b netcode.Bundle) {
	if e.lossRate > 0 && e.rng.Float64() < e.lossRate {
		e.lost++
		return
	}
	latency := e.minLatency
	if e.jitter > 0 {
		latency += e.rng.IntN(e.jitter + 1)
	}
	e.queue = append(e.queue, inFlight{bundle: b, deliverAt: e.tick + latency})
}

// Tick 推进一个时钟 tick，返回此刻到达的所有消息（保持发送顺序）。
func (e *Emulator) Tick() []netcode.Bundle {
	e.tick++
	ready := make([]netcode.Bundle, 0)
	// 使用独立切片保留未投递消息，避免就地复用时覆盖尚未遍历的元素。
	kept := make([]inFlight, 0, len(e.queue))
	for _, m := range e.queue {
		if !e.blocked && m.deliverAt <= e.tick {
			ready = append(ready, m.bundle)
			e.delivered++
		} else {
			kept = append(kept, m)
		}
	}
	e.queue = kept
	return ready
}

// SetBlocked 开关链路中断。中断期间已在途消息滞留，恢复后按原序投递。
func (e *Emulator) SetBlocked(blocked bool) { e.blocked = blocked }

// Tick 返回当前时钟。
func (e *Emulator) Now() int { return e.tick }

// Lost 返回累计丢弃包数。
func (e *Emulator) Lost() int { return e.lost }

// Delivered 返回累计投递包数。
func (e *Emulator) Delivered() int { return e.delivered }

// InFlight 返回尚未投递的消息数。
func (e *Emulator) InFlight() int { return len(e.queue) }
