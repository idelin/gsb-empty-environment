package netsec

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// OAuthError 是 RFC 8628 第 3.5 节定义的设备访问令牌响应错误。
type OAuthError struct {
	Code        string
	Description string
}

func (e *OAuthError) Error() string {
	return "netsec: " + e.Code + ": " + e.Description
}

var (
	// ErrAuthorizationPending 表示用户尚未完成授权，客户端应按当前间隔继续轮询。
	ErrAuthorizationPending = &OAuthError{Code: "authorization_pending", Description: "the authorization request is still pending"}
	// ErrSlowDown 表示轮询过于频繁，客户端必须把间隔再加大 5 秒。
	ErrSlowDown = &OAuthError{Code: "slow_down", Description: "polling too frequently, increase the interval"}
	// ErrAccessDenied 表示用户拒绝了授权请求。
	ErrAccessDenied = &OAuthError{Code: "access_denied", Description: "the user denied the authorization request"}
	// ErrInvalidGrant 表示 device_code 未知、已兑换或已失效。
	ErrInvalidGrant = &OAuthError{Code: "invalid_grant", Description: "the device code is unknown, already redeemed or expired"}
)

// 轮询管控默认参数。
const (
	DefaultPollInterval = 5 * time.Second // 设备轮询的初始间隔
	DefaultSlowDownStep = 5 * time.Second // 每次 slow_down 增加的间隔
)

// Token 是授权成功后签发的访问令牌。
type Token struct {
	AccessToken string
	TokenType   string
}

type deviceStatus int

const (
	statusPending deviceStatus = iota
	statusApproved
	statusDenied
)

type deviceSession struct {
	status    deviceStatus
	interval  time.Duration
	lastPoll  time.Time
	hasPolled bool
	redeemed  bool
}

// TokenManager 管控设备令牌端点（RFC 8628 第 3.4 节）的轮询频率与一次性兑换。
// 并发安全。
type TokenManager struct {
	now             func() time.Time
	defaultInterval time.Duration
	slowDownStep    time.Duration
	mu              sync.Mutex
	sessions        map[string]*deviceSession
}

// TokenManagerOption 自定义 TokenManager 参数。
type TokenManagerOption func(*TokenManager)

// WithTokenClock 注入时钟，便于测试。
func WithTokenClock(now func() time.Time) TokenManagerOption {
	return func(m *TokenManager) { m.now = now }
}

// NewTokenManager 创建使用默认轮询参数的 TokenManager。
func NewTokenManager(opts ...TokenManagerOption) *TokenManager {
	m := &TokenManager{
		now:             time.Now,
		defaultInterval: DefaultPollInterval,
		slowDownStep:    DefaultSlowDownStep,
		sessions:        make(map[string]*deviceSession),
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// RegisterDevice 为一次设备授权流程登记 device_code，初始状态为待授权。
func (m *TokenManager) RegisterDevice(deviceCode string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[deviceCode] = &deviceSession{interval: m.defaultInterval}
}

// Approve 标记用户已同意该 device_code 对应的授权请求。
func (m *TokenManager) Approve(deviceCode string) {
	m.setStatus(deviceCode, statusApproved)
}

// Deny 标记用户拒绝了该 device_code 对应的授权请求。
func (m *TokenManager) Deny(deviceCode string) {
	m.setStatus(deviceCode, statusDenied)
}

func (m *TokenManager) setStatus(deviceCode string, status deviceStatus) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[deviceCode]; ok && !s.redeemed {
		s.status = status
	}
}

// Interval 返回该 device_code 当前要求客户端遵守的轮询间隔。
func (m *TokenManager) Interval(deviceCode string) (time.Duration, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[deviceCode]
	if !ok {
		return 0, false
	}
	return s.interval, true
}

// Poll 处理设备用 device_code 换取令牌的请求。
//
// 早于当前间隔的轮询返回 ErrSlowDown 并把间隔加大 5 秒；
// 待授权返回 ErrAuthorizationPending；被拒绝返回 ErrAccessDenied；
// 已批准则签发令牌并使 device_code 立即失效，再次使用返回 ErrInvalidGrant。
func (m *TokenManager) Poll(deviceCode string) (Token, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sessions[deviceCode]
	if !ok || s.redeemed {
		return Token{}, ErrInvalidGrant
	}
	if s.status == statusDenied {
		return Token{}, ErrAccessDenied
	}

	now := m.now()
	if s.hasPolled && now.Sub(s.lastPoll) < s.interval {
		s.interval += m.slowDownStep
		s.lastPoll = now
		return Token{}, ErrSlowDown
	}
	s.lastPoll = now
	s.hasPolled = true

	if s.status == statusPending {
		return Token{}, ErrAuthorizationPending
	}

	token, err := issueToken()
	if err != nil {
		return Token{}, err
	}
	s.redeemed = true
	return token, nil
}

func issueToken() (Token, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return Token{}, err
	}
	return Token{AccessToken: hex.EncodeToString(buf), TokenType: "Bearer"}, nil
}
