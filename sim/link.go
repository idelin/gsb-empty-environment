package sim

import (
	"errors"
	"math/rand"

	"reorder"
)

// LinkConfig 描述一条随机链路：均匀分布的传播延迟（产生乱序）、
// 独立丢包率与独立重复率。所有概率取值范围为 [0,1]。
type LinkConfig struct {
	MinDelay int64   // 最小传播延迟（tick，含），必须 >= 0
	MaxDelay int64   // 最大传播延迟（tick，含），必须 >= MinDelay
	LossProb float64 // 丢包概率
	DupProb  float64 // 产生一个重复副本的概率
}

// Link 是建立在 Scheduler 之上的模拟链路。
type Link struct {
	cfg LinkConfig
	rng *rand.Rand
}

// NewLink 用固定随机种子创建链路，便于复现实验。
func NewLink(cfg LinkConfig, seed int64) (*Link, error) {
	if cfg.MinDelay < 0 || cfg.MaxDelay < cfg.MinDelay {
		return nil, errors.New("sim: invalid delay range")
	}
	if cfg.LossProb < 0 || cfg.LossProb > 1 || cfg.DupProb < 0 || cfg.DupProb > 1 {
		return nil, errors.New("sim: probability must be in [0,1]")
	}
	return &Link{cfg: cfg, rng: rand.New(rand.NewSource(seed))}, nil
}

// Send 在调度器上安排 p 的到达事件。sendTick 为发送时刻，接收方
// 回调 recv 在到达 tick 被触发。链路不保证按序到达。
func (l *Link) Send(s *Scheduler, sendTick int64, p reorder.Packet, recv func(reorder.Packet)) error {
	if l.rng.Float64() < l.cfg.LossProb {
		return nil // 丢包：不安排任何到达事件
	}
	span := int(l.cfg.MaxDelay - l.cfg.MinDelay + 1)
	delay := l.cfg.MinDelay + int64(l.rng.Intn(span))
	arrive := sendTick + delay
	if err := s.Schedule(arrive, func(int64) []Event {
		recv(p)
		return nil
	}); err != nil {
		return err
	}
	if l.rng.Float64() < l.cfg.DupProb {
		// 重复副本再滞后 1~3 个 tick 到达。
		dupDelay := delay + 1 + int64(l.rng.Intn(3))
		if err := s.Schedule(sendTick+dupDelay, func(int64) []Event {
			recv(p)
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

// ScriptLink 是确定性脚本链路，用于演示与精确测试：每个包的延迟、
// 丢弃由调用方函数给出，不使用随机数。
type ScriptLink struct {
	// Offset 返回发送后多少 tick 到达；返回值必须 >= 0。
	Offset func(p reorder.Packet) int64
	// Drop 返回 true 时该包被丢弃（不安排到达）。可为 nil。
	Drop func(p reorder.Packet) bool
}

// Send 按脚本在调度器上安排到达事件。
func (l ScriptLink) Send(s *Scheduler, sendTick int64, p reorder.Packet, recv func(reorder.Packet)) error {
	if l.Drop != nil && l.Drop(p) {
		return nil
	}
	if l.Offset == nil {
		return errors.New("sim: ScriptLink missing Offset")
	}
	offset := l.Offset(p)
	if offset < 0 {
		return errors.New("sim: negative scripted offset")
	}
	return s.Schedule(sendTick+offset, func(int64) []Event {
		recv(p)
		return nil
	})
}
