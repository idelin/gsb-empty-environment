package retry

import (
	"sync"
	"time"
)

// BreakerState 是熔断器的三态。
type BreakerState int

const (
	// BreakerClosed 正常放行，记录连续故障。
	BreakerClosed BreakerState = iota
	// BreakerOpen 快速失败，冷却结束后转入 HalfOpen。
	BreakerOpen
	// BreakerHalfOpen 放行少量探测请求，成功则关闭、失败则重新打开。
	BreakerHalfOpen
)

func (s BreakerState) String() string {
	switch s {
	case BreakerClosed:
		return "Closed"
	case BreakerOpen:
		return "HalfOpen"
	default:
		return "Open"
	}
}

// BreakerConfig 配置连续故障熔断器。
type BreakerConfig struct {
	// FailureThreshold 触发熔断的连续故障次数；<=0 表示禁用熔断。
	FailureThreshold int
	// OpenCoolDown Open 态持续时间，过后转为 HalfOpen。
	OpenCoolDown time.Duration
	// HalfOpenMaxConcurrent HalfOpen 态同时允许的探测请求数。
	HalfOpenMaxConcurrent int
}

// Breaker 是线程安全的连续故障熔断器。
type Breaker struct {
	cfg BreakerConfig

	mu            sync.Mutex
	state         BreakerState
	consecFail    int
	openedAt      time.Time
	halfOpenInUse int
	now           func() time.Time
}

// NewBreaker 构造熔断器。
func NewBreaker(cfg BreakerConfig) *Breaker {
	return &Breaker{cfg: cfg, state: BreakerClosed, now: time.Now}
}

func (b *Breaker) withClock(now func() time.Time) *Breaker {
	b.now = now
	return b
}

// State 返回当前状态，并在 Open 冷却到期时自动迁移到 HalfOpen。
func (b *Breaker) State() BreakerState {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refreshLocked()
	return b.state
}

func (b *Breaker) refreshLocked() {
	if b.state == BreakerOpen && b.cfg.OpenCoolDown > 0 &&
		b.now().Sub(b.openedAt) >= b.cfg.OpenCoolDown {
		b.state = BreakerHalfOpen
		b.halfOpenInUse = 0
	}
}

// Allow 判断一个新请求（含重试）是否可通过熔断器。
// HalfOpen 态下会占用一个探测名额，须在请求结束时调用 Release。
func (b *Breaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refreshLocked()
	switch b.state {
	case BreakerClosed:
		return true
	case BreakerOpen:
		return false
	default: // HalfOpen
		maxProbe := b.cfg.HalfOpenMaxConcurrent
		if maxProbe <= 0 {
			maxProbe = 1
		}
		if b.halfOpenInUse >= maxProbe {
			return false
		}
		b.halfOpenInUse++
		return true
	}
}

// Release 归还 HalfOpen 探测名额。
func (b *Breaker) Release() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.halfOpenInUse > 0 {
		b.halfOpenInUse--
	}
}

// OnSuccess 记录一次成功：Closed 态清零连续故障；HalfOpen 态探测成功则关闭熔断。
func (b *Breaker) OnSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case BreakerClosed:
		b.consecFail = 0
	case BreakerHalfOpen:
		b.state = BreakerClosed
		b.consecFail = 0
		b.halfOpenInUse = 0
	}
}

// OnFailure 记录一次失败：连续故障达到阈值则打开熔断。
func (b *Breaker) OnFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.consecFail++
	if b.state == BreakerHalfOpen {
		b.tripLocked()
		return
	}
	if b.cfg.FailureThreshold > 0 && b.consecFail >= b.cfg.FailureThreshold {
		b.tripLocked()
	}
}

func (b *Breaker) tripLocked() {
	b.state = BreakerOpen
	b.openedAt = b.now()
	b.halfOpenInUse = 0
}

// ConsecutiveFailures 返回当前连续故障计数。
func (b *Breaker) ConsecutiveFailures() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.consecFail
}
