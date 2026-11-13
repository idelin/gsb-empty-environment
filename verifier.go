package netsec

import (
	"errors"
	"sync"
	"time"
)

var (
	// ErrSourceLocked 表示来源 IP 因错误次数超限被锁定，对所有 user_code 一视同仁。
	ErrSourceLocked = errors.New("netsec: source locked after too many failed attempts")
	// ErrInvalidUserCode 表示 user_code 不存在或已作废。
	ErrInvalidUserCode = errors.New("netsec: invalid or invalidated user_code")
)

// Verifier 默认管控参数。
const (
	DefaultIPWindow        = 5 * time.Minute  // 同一来源错误计数窗口
	DefaultMaxIPErrors     = 5                // 窗口内允许的最大错误次数
	DefaultLockDuration    = 15 * time.Minute // 超限后的锁定时长
	DefaultMaxCodeFailures = 5                // 单个 user_code 作废前允许的错误尝试次数
)

type ipState struct {
	failures    []time.Time
	lockedUntil time.Time
}

type codeState struct {
	registered bool
	failures   int
	burned     bool
}

// Verifier 校验设备授权端点收到的 user_code，实施来源限流与单码作废。
// 并发安全。
type Verifier struct {
	now             func() time.Time
	window          time.Duration
	maxIPErrors     int
	lockDuration    time.Duration
	maxCodeFailures int

	mu    sync.Mutex
	ips   map[string]*ipState
	codes map[string]*codeState
}

// VerifierOption 自定义 Verifier 参数。
type VerifierOption func(*Verifier)

// WithClock 注入时钟，便于测试。
func WithClock(now func() time.Time) VerifierOption {
	return func(v *Verifier) { v.now = now }
}

// NewVerifier 创建使用默认管控参数的 Verifier。
func NewVerifier(opts ...VerifierOption) *Verifier {
	v := &Verifier{
		now:             time.Now,
		window:          DefaultIPWindow,
		maxIPErrors:     DefaultMaxIPErrors,
		lockDuration:    DefaultLockDuration,
		maxCodeFailures: DefaultMaxCodeFailures,
		ips:             make(map[string]*ipState),
		codes:           make(map[string]*codeState),
	}
	for _, opt := range opts {
		opt(v)
	}
	return v
}

// RegisterUserCode 登记一个待用户确认的 user_code（须为规范形式，见 GenerateUserCode）。
// 已被作废的同名 code 不会复活。
func (v *Verifier) RegisterUserCode(code string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	cs, ok := v.codes[code]
	if ok && cs.burned {
		return
	}
	if !ok {
		cs = &codeState{}
		v.codes[code] = cs
	}
	cs.registered = true
}

// Verify 校验来自 sourceIP 的 user_code 输入。
// 输入忽略连字符与大小写。来源被锁定时返回 ErrSourceLocked；
// 输入格式非法、code 不存在或已作废时记录一次来源错误并返回 ErrInvalidUserCode。
func (v *Verifier) Verify(sourceIP, input string) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	now := v.now()
	ip := v.ips[sourceIP]
	if ip == nil {
		ip = &ipState{}
		v.ips[sourceIP] = ip
	}
	if now.Before(ip.lockedUntil) {
		return ErrSourceLocked
	}

	code, err := NormalizeUserCode(input)
	if err != nil {
		if v.recordIPFailure(ip, now) {
			return ErrSourceLocked
		}
		return ErrInvalidUserCode
	}
	cs, ok := v.codes[code]
	if !ok {
		cs = &codeState{}
		v.codes[code] = cs
	}
	if cs.burned || !cs.registered {
		cs.failures++
		if cs.failures >= v.maxCodeFailures {
			cs.burned = true
		}
		if v.recordIPFailure(ip, now) {
			return ErrSourceLocked
		}
		return ErrInvalidUserCode
	}
	return nil
}

// recordIPFailure 在滑动窗口内记录一次来源错误，超过上限则锁定来源。
// 返回值为 true 表示本次错误触发了锁定。
func (v *Verifier) recordIPFailure(ip *ipState, now time.Time) bool {
	cutoff := now.Add(-v.window)
	kept := ip.failures[:0]
	for _, t := range ip.failures {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	ip.failures = append(kept, now)
	if len(ip.failures) > v.maxIPErrors {
		ip.lockedUntil = now.Add(v.lockDuration)
		ip.failures = ip.failures[:0]
		return true
	}
	return false
}
