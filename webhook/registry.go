package webhook

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// DefaultTTL 是一次握手验证的有效期；超时未重新验证即暂停投递。
const DefaultTTL = 24 * time.Hour

// Subscription 是一条已登记的订阅。
type Subscription struct {
	// URL 是回调地址。
	URL string
	// Host 是登记地址的主机名，用于响应主机一致性校验留档。
	Host string
	// Rate 是双方速率取小后的实际投递上限。
	Rate Rate
	// VerifiedAt 是最近一次成功验证的时间。
	VerifiedAt time.Time
	// ExpiresAt 是验证到期时间，到期后投递暂停。
	ExpiresAt time.Time
}

// Expired 判断订阅在 now 时刻是否已过验证有效期。
func (s Subscription) Expired(now time.Time) bool {
	return !now.Before(s.ExpiresAt)
}

// Registry 是并发安全的订阅注册表。
type Registry struct {
	mu   sync.RWMutex
	subs map[string]Subscription
	ttl  time.Duration
	now  func() time.Time
}

// NewRegistry 创建注册表，ttl 为零值时使用 DefaultTTL。
func NewRegistry(ttl time.Duration) *Registry {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &Registry{
		subs: make(map[string]Subscription),
		ttl:  ttl,
		now:  func() time.Time { return time.Now().UTC() },
	}
}

// Add 登记一条新订阅；URL 已存在时返回 ErrExistingSubscription。
func (r *Registry) Add(result Result) (Subscription, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := normalizeURL(result.URL)
	if _, ok := r.subs[key]; ok {
		return Subscription{}, fmt.Errorf("%w: %s", ErrExistingSubscription, result.URL)
	}
	sub := r.buildSubscription(result)
	r.subs[key] = sub
	return sub, nil
}

// Update 用一次重新验证的结果覆盖既有订阅并续期；不存在则报错。
func (r *Registry) Update(result Result) (Subscription, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := normalizeURL(result.URL)
	if _, ok := r.subs[key]; !ok {
		return Subscription{}, fmt.Errorf("%w: %s", ErrSubscriptionNotFound, result.URL)
	}
	sub := r.buildSubscription(result)
	r.subs[key] = sub
	return sub, nil
}

// Get 返回订阅；不存在返回 ErrSubscriptionNotFound。
func (r *Registry) Get(callbackURL string) (Subscription, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	sub, ok := r.subs[normalizeURL(callbackURL)]
	if !ok {
		return Subscription{}, fmt.Errorf("%w: %s", ErrSubscriptionNotFound, callbackURL)
	}
	return sub, nil
}

// Remove 删除订阅，删除不存在的 URL 同样返回 ErrSubscriptionNotFound。
func (r *Registry) Remove(callbackURL string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := normalizeURL(callbackURL)
	if _, ok := r.subs[key]; !ok {
		return fmt.Errorf("%w: %s", ErrSubscriptionNotFound, callbackURL)
	}
	delete(r.subs, key)
	return nil
}

// AllowedToDeliver 判断此刻是否允许向该地址投递：
// 订阅不存在或验证已过期（24 小时未重新验证）都返回错误并暂停投递。
func (r *Registry) AllowedToDeliver(callbackURL string) (Subscription, error) {
	sub, err := r.Get(callbackURL)
	if err != nil {
		return Subscription{}, err
	}
	if sub.Expired(r.now()) {
		return sub, fmt.Errorf("%w: %s", ErrSubscriptionExpired, callbackURL)
	}
	return sub, nil
}

// DueForReverify 返回所有已到期、需要重新验证的订阅。
func (r *Registry) DueForReverify() []Subscription {
	r.mu.RLock()
	defer r.mu.RUnlock()

	now := r.now()
	due := make([]Subscription, 0)
	for _, sub := range r.subs {
		if sub.Expired(now) {
			due = append(due, sub)
		}
	}
	return due
}

func (r *Registry) buildSubscription(result Result) Subscription {
	verifiedAt := result.VerifiedAt
	if verifiedAt.IsZero() {
		verifiedAt = r.now()
	}
	return Subscription{
		URL:        result.URL,
		Host:       result.Host,
		Rate:       result.EffectiveRate,
		VerifiedAt: verifiedAt,
		ExpiresAt:  verifiedAt.Add(r.ttl),
	}
}

func normalizeURL(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

// Manager 组合验证器与注册表，提供登记、投递准入与重新验证的一体化入口。
// HandshakeVerifier 抽象握手过程，*Verifier 是其默认实现，测试中可替换为假实现。
type HandshakeVerifier interface {
	Verify(ctx context.Context, callbackURL string) (Result, error)
}

type Manager struct {
	verifier HandshakeVerifier
	registry *Registry
}

// NewManager 组装 Manager。
func NewManager(verifier HandshakeVerifier, registry *Registry) *Manager {
	return &Manager{verifier: verifier, registry: registry}
}

// Register 执行握手并登记新订阅；任一步失败都不会写入注册表。
func (m *Manager) Register(ctx context.Context, callbackURL string) (Subscription, error) {
	result, err := m.verifier.Verify(ctx, callbackURL)
	if err != nil {
		return Subscription{}, err
	}
	return m.registry.Add(result)
}

// Reverify 对既有订阅重新执行握手，成功后续期，失败则保持原订阅（到期即暂停投递）。
func (m *Manager) Reverify(ctx context.Context, callbackURL string) (Subscription, error) {
	if _, err := m.registry.Get(callbackURL); err != nil {
		return Subscription{}, err
	}
	result, err := m.verifier.Verify(ctx, callbackURL)
	if err != nil {
		return Subscription{}, err
	}
	return m.registry.Update(result)
}

// AllowedToDeliver 委托给注册表做投递准入判断。
func (m *Manager) AllowedToDeliver(callbackURL string) (Subscription, error) {
	return m.registry.AllowedToDeliver(callbackURL)
}

// ReverifyDue 对所有验证到期的订阅逐一重新验证，返回每个地址的失败原因。
func (m *Manager) ReverifyDue(ctx context.Context) map[string]error {
	failures := make(map[string]error)
	for _, sub := range m.registry.DueForReverify() {
		if _, err := m.Reverify(ctx, sub.URL); err != nil {
			failures[sub.URL] = err
		}
	}
	return failures
}
