// Package adaptive 实现自适应动态阈值伸缩与负载感知控制。
//
// 控制器根据外部压力与环境指标（CPU、P99 延迟、错误率、在途请求数）
// 动态调节内部阈值（如最大并发数）：
//   - 压力超目标或出现错误：乘性快速回退 + 冷却期，规避雪崩；
//   - 压力健康且需求饱和：按余量比例加性爬坡，最大化资源利用；
//   - 迟滞带 + 冷却期抑制抖动；阈值始终钳制在 [Min, Max]。
//
// 全部状态为定长（EMA），不积累历史，无线程泄漏；导出方法均并发安全。
package adaptive

import (
	"errors"
	"math"
	"sync"
	"time"
)

// Metrics 外部压力与环境指标采样。
type Metrics struct {
	InFlight   int     // 当前在途请求数
	CPU        float64 // CPU 利用率 [0,1]
	LatencyP99 float64 // P99 延迟（秒）
	ErrorRate  float64 // 错误率 [0,1]
}

// valid 防御性校验：拒绝 NaN 与负值，防止脏指标污染控制器。
func (m Metrics) valid() bool {
	if m.InFlight < 0 {
		return false
	}
	for _, v := range []float64{m.CPU, m.LatencyP99, m.ErrorRate} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			return false
		}
	}
	return true
}

// Config 控制器配置。
type Config struct {
	Min            float64       // 阈值下界（>=0）
	Max            float64       // 阈值上界（>Min）
	TargetCPU      float64       // 目标 CPU 利用率 (0,1]
	LatencyGoal    float64       // 延迟目标（秒），>0
	DecreaseFactor float64       // 乘性回退因子 (0,1)，越小回退越猛
	IncreaseRatio  float64       // 加性爬坡比例 (0,1]，相对 (Max-Min) 满余量步长
	Cooldown       time.Duration // 回退后的冷却期（>=0），期间禁止上调
	Smoothing      float64       // 压力 EMA 平滑系数 (0,1]
}

// DefaultConfig 返回一组保守的默认参数。
func DefaultConfig() Config {
	return Config{
		Min:            4,
		Max:            512,
		TargetCPU:      0.75,
		LatencyGoal:    0.25,
		DecreaseFactor: 0.5,
		IncreaseRatio:  0.05,
		Cooldown:       2 * time.Second,
		Smoothing:      0.4,
	}
}

func (c Config) validate() error {
	switch {
	case math.IsNaN(c.Min) || math.IsNaN(c.Max) || c.Min < 0 || c.Max <= c.Min:
		return errors.New("adaptive: require 0 <= Min < Max")
	case c.TargetCPU <= 0 || c.TargetCPU > 1:
		return errors.New("adaptive: require 0 < TargetCPU <= 1")
	case c.LatencyGoal <= 0 || math.IsNaN(c.LatencyGoal):
		return errors.New("adaptive: require LatencyGoal > 0")
	case c.DecreaseFactor <= 0 || c.DecreaseFactor >= 1:
		return errors.New("adaptive: require 0 < DecreaseFactor < 1")
	case c.IncreaseRatio <= 0 || c.IncreaseRatio > 1:
		return errors.New("adaptive: require 0 < IncreaseRatio <= 1")
	case c.Cooldown < 0:
		return errors.New("adaptive: require Cooldown >= 0")
	case c.Smoothing <= 0 || c.Smoothing > 1:
		return errors.New("adaptive: require 0 < Smoothing <= 1")
	}
	return nil
}

// 压力超过该值即触发回退；低于 lowWatermark 才允许爬坡（迟滞带）。
const (
	highWatermark = 1.0
	lowWatermark  = 0.8
	// 错误率硬阈值：一旦越线立即回退（不等 EMA 爬升），快速规避雪崩。
	errorRateTrip = 0.02
	// 需求饱和判定：在途请求达到阈值的该比例才允许上调（负载感知）。
	demandSaturation = 0.9
)

// Controller 自适应动态阈值控制器。零值不可用，请用 NewController。
type Controller struct {
	mu            sync.Mutex
	cfg           Config
	threshold     float64
	pressure      float64 // 归一化压力 EMA，1.0 = 目标负载
	cooldownUntil time.Time
	now           func() time.Time // 可注入时钟，便于测试
}

// NewController 创建控制器，初始阈值为 Min（冷启动保守，避免雪崩）。
func NewController(cfg Config) (*Controller, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Controller{cfg: cfg, threshold: cfg.Min, now: time.Now}, nil
}

// Snapshot 控制器状态快照。
type Snapshot struct {
	Threshold float64
	Pressure  float64
}

// State 返回当前阈值与压力（并发安全）。
func (c *Controller) State() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Snapshot{Threshold: c.threshold, Pressure: c.pressure}
}

// Threshold 返回当前阈值（并发安全）。
func (c *Controller) Threshold() float64 { return c.State().Threshold }

// Update 摄入一次指标采样并调节阈值，返回最新阈值（并发安全）。
func (c *Controller) Update(m Metrics) float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !m.valid() {
		return c.threshold // 脏指标直接丢弃
	}
	// 归一化压力：CPU 与延迟取较大者，1.0 表示达到目标负载。
	p := math.Max(m.CPU/c.cfg.TargetCPU, m.LatencyP99/c.cfg.LatencyGoal)
	a := c.cfg.Smoothing
	c.pressure = a*p + (1-a)*c.pressure
	now := c.now()
	switch {
	case c.pressure > highWatermark || m.ErrorRate > errorRateTrip:
		// 过载或出错：乘性快速回退并进入冷却期，规避雪崩。
		c.threshold = math.Max(c.cfg.Min, c.threshold*c.cfg.DecreaseFactor)
		c.cooldownUntil = now.Add(c.cfg.Cooldown)
	case c.pressure < lowWatermark &&
		!now.Before(c.cooldownUntil) &&
		float64(m.InFlight) >= c.threshold*demandSaturation:
		// 健康且需求饱和：按余量比例加性爬坡（负载感知，最大化利用）。
		headroom := 1 - c.pressure
		c.threshold = math.Min(c.cfg.Max,
			c.threshold+(c.cfg.Max-c.cfg.Min)*c.cfg.IncreaseRatio*headroom)
	}
	return c.threshold
}
