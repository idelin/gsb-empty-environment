package netsec

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"sync"
)

// 订阅相关的哨兵错误。
var (
	ErrEmptyTenant           = errors.New("netsec: tenant id is required")
	ErrEmptyVAPIDFingerprint = errors.New("netsec: VAPID public key fingerprint is required")
	ErrUnknownSubscription   = errors.New("netsec: unknown subscription id")
	ErrTenantMismatch        = errors.New("netsec: subscription belongs to another tenant")
)

// SubscriptionKeys 为 RFC 8291 加密所需的浏览器侧公钥材料。
type SubscriptionKeys struct {
	// P256DH 为 65 字节未压缩 P-256 公钥（用户代理公钥 ua_public）。
	P256DH []byte
	// Auth 为 16 字节认证密钥。
	Auth []byte
}

// Subscription 是经过安全校验、绑定租户与 VAPID 公钥指纹的推送订阅。
type Subscription struct {
	// ID 为服务端生成的不透明订阅标识。
	ID string
	// TenantID 为订阅归属租户，跨租户访问一律拒绝。
	TenantID string
	// Endpoint 为已校验的推送端点（规范化拷贝）。
	Endpoint *url.URL
	// VAPIDFingerprint 为创建订阅时绑定的 VAPID 公钥 SHA-256 指纹。
	VAPIDFingerprint [32]byte
	// Keys 为用户代理加密公钥材料，可为空（仅支持未加密推送时）。
	Keys SubscriptionKeys
}

// CreateSubscriptionInput 为创建订阅的入参。
type CreateSubscriptionInput struct {
	TenantID         string
	RawEndpoint      string
	VAPIDFingerprint [32]byte
	Keys             SubscriptionKeys
}

// Registry 是按租户隔离的内存订阅注册表。
type Registry struct {
	guard *Guard

	mu    sync.RWMutex
	byID  map[string]*Subscription
	index map[string]map[string]struct{} // tenantID -> set(subscriptionID)
}

// NewRegistry 创建依赖给定安全 Guard 的订阅注册表。
func NewRegistry(guard *Guard) *Registry {
	if guard == nil {
		panic("netsec: NewRegistry requires a non-nil Guard")
	}
	return &Registry{
		guard: guard,
		byID:  make(map[string]*Subscription),
		index: make(map[string]map[string]struct{}),
	}
}

// Create 校验端点并创建租户绑定的订阅。
func (r *Registry) Create(ctx context.Context, input CreateSubscriptionInput) (*Subscription, error) {
	if input.TenantID == "" {
		return nil, ErrEmptyTenant
	}
	if input.VAPIDFingerprint == [32]byte{} {
		return nil, ErrEmptyVAPIDFingerprint
	}
	endpoint, err := r.guard.ValidateEndpoint(ctx, input.RawEndpoint)
	if err != nil {
		return nil, err
	}
	id, err := newSubscriptionID()
	if err != nil {
		return nil, err
	}
	sub := &Subscription{
		ID:               id,
		TenantID:         input.TenantID,
		Endpoint:         cloneURL(endpoint),
		VAPIDFingerprint: input.VAPIDFingerprint,
		Keys:             cloneKeys(input.Keys),
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byID[id] = sub
	if r.index[input.TenantID] == nil {
		r.index[input.TenantID] = make(map[string]struct{})
	}
	r.index[input.TenantID][id] = struct{}{}
	return cloneSubscription(sub), nil
}

// Get 返回指定租户名下的订阅；跨租户访问统一返回 ErrUnknownSubscription，
// 避免向其他租户泄漏订阅是否存在。
func (r *Registry) Get(tenantID, subscriptionID string) (*Subscription, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	sub, ok := r.byID[subscriptionID]
	if !ok {
		return nil, ErrUnknownSubscription
	}
	if sub.TenantID != tenantID {
		return nil, ErrUnknownSubscription
	}
	return cloneSubscription(sub), nil
}

// ListByTenant 列出某租户的全部订阅。
func (r *Registry) ListByTenant(tenantID string) []*Subscription {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := r.index[tenantID]
	out := make([]*Subscription, 0, len(ids))
	for id := range ids {
		if sub, ok := r.byID[id]; ok {
			out = append(out, cloneSubscription(sub))
		}
	}
	return out
}

// Remove 删除租户名下的订阅；跨租户删除同样按不存在处理。
func (r *Registry) Remove(tenantID, subscriptionID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	sub, ok := r.byID[subscriptionID]
	if !ok || sub.TenantID != tenantID {
		return ErrUnknownSubscription
	}
	delete(r.byID, subscriptionID)
	delete(r.index[tenantID], subscriptionID)
	if len(r.index[tenantID]) == 0 {
		delete(r.index, tenantID)
	}
	return nil
}

// CheckTenant 在不暴露订阅存在性的前提下判定订阅归属。
func (r *Registry) CheckTenant(tenantID, subscriptionID string) error {
	_, err := r.Get(tenantID, subscriptionID)
	return err
}

func newSubscriptionID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("netsec: generate subscription id: %w", err)
	}
	// RFC 4122 版本 4 / 变体位 10xx。
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return hex.EncodeToString(raw[:]), nil
}

func cloneURL(u *url.URL) *url.URL {
	if u == nil {
		return nil
	}
	copyURL := *u
	return &copyURL
}

func cloneKeys(keys SubscriptionKeys) SubscriptionKeys {
	return SubscriptionKeys{
		P256DH: append([]byte(nil), keys.P256DH...),
		Auth:   append([]byte(nil), keys.Auth...),
	}
}

func cloneSubscription(sub *Subscription) *Subscription {
	return &Subscription{
		ID:               sub.ID,
		TenantID:         sub.TenantID,
		Endpoint:         cloneURL(sub.Endpoint),
		VAPIDFingerprint: sub.VAPIDFingerprint,
		Keys:             cloneKeys(sub.Keys),
	}
}
