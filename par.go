// Package netsec 提供 OAuth 推送授权请求（PAR, RFC 9126 风格）的
// 安全请求对象组件，供 Go 授权服务器导入调用。
//
// 安全属性：
//   - request_uri 绑定 client_id、请求参数摘要、用户会话与短有效期；
//   - 兑换时必须同一 client、同一会话、同一组参数，且一次性消费；
//   - 并发兑换只有一个成功；
//   - 请求对象内容入库后不可被后续修改；
//   - scope 与 redirect_uri 按原请求核对。
package netsec

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// 组件返回的错误，调用方可用 errors.Is 判定。
var (
	// ErrRequestNotFound 表示 request_uri 不存在、已被消费或已过期被清除。
	ErrRequestNotFound = errors.New("par: request_uri not found or already consumed")
	// ErrRequestExpired 表示请求对象已超过有效期。
	ErrRequestExpired = errors.New("par: request object expired")
	// ErrClientMismatch 表示兑换方 client 与推送方 client 不一致。
	ErrClientMismatch = errors.New("par: client_id mismatch")
	// ErrSessionMismatch 表示兑换时的用户会话与推送时不一致。
	ErrSessionMismatch = errors.New("par: user session mismatch")
	// ErrParamMismatch 表示兑换时提交的参数与推送时的摘要不一致。
	ErrParamMismatch = errors.New("par: authorization parameters mismatch")
	// ErrScopeMismatch 表示兑换时的 scope 与原请求不一致。
	ErrScopeMismatch = errors.New("par: scope mismatch")
	// ErrRedirectMismatch 表示兑换时的 redirect_uri 与原请求不一致。
	ErrRedirectMismatch = errors.New("par: redirect_uri mismatch")
)

// RequestURIScheme 是 request_uri 的 URN 前缀（RFC 9126）。
const RequestURIScheme = "urn:ietf:params:oauth:request_uri:"

// DefaultTTL 是请求对象的默认有效期。
const DefaultTTL = 60 * time.Second

// RequestObject 是入库后的授权请求快照。其内容在创建时冻结，
// 所有字段不可导出，访问器返回副本，保证不可被后续修改。
type RequestObject struct {
	clientID    string
	sessionID   string
	params      url.Values // 冻结副本
	digest      string     // 参数的规范摘要
	scope       string
	redirectURI string
	expiresAt   time.Time
}

// ClientID 返回推送该请求的 client_id。
func (r *RequestObject) ClientID() string { return r.clientID }

// SessionID 返回推送该请求时绑定的用户会话标识。
func (r *RequestObject) SessionID() string { return r.sessionID }

// Scope 返回原请求的 scope。
func (r *RequestObject) Scope() string { return r.scope }

// RedirectURI 返回原请求的 redirect_uri。
func (r *RequestObject) RedirectURI() string { return r.redirectURI }

// ExpiresAt 返回请求对象的过期时间。
func (r *RequestObject) ExpiresAt() time.Time { return r.expiresAt }

// Digest 返回原请求参数的规范摘要（hex 编码的 SHA-256）。
func (r *RequestObject) Digest() string { return r.digest }

// Params 返回原请求参数的深拷贝，修改返回值不影响存储对象。
func (r *RequestObject) Params() url.Values { return cloneValues(r.params) }

// Store 是请求对象的并发安全存储，负责推送与一次性兑换。
type Store struct {
	mu      sync.Mutex
	entries map[string]*RequestObject
	ttl     time.Duration
	now     func() time.Time // 便于测试注入时钟
}

// Option 用于配置 Store。
type Option func(*Store)

// WithTTL 覆盖默认有效期。
func WithTTL(ttl time.Duration) Option {
	return func(s *Store) {
		if ttl > 0 {
			s.ttl = ttl
		}
	}
}

// NewStore 创建请求对象存储。
func NewStore(opts ...Option) *Store {
	s := &Store{
		entries: make(map[string]*RequestObject),
		ttl:     DefaultTTL,
		now:     time.Now,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Push 将授权请求参数与 client、会话绑定后入库，返回 request_uri。
// params 在入库时被深拷贝并冻结，调用方后续修改不影响存储内容。
func (s *Store) Push(clientID, sessionID string, params url.Values) (string, error) {
	if clientID == "" {
		return "", errors.New("par: client_id is required")
	}
	if sessionID == "" {
		return "", errors.New("par: session id is required")
	}
	frozen := cloneValues(params)
	obj := &RequestObject{
		clientID:    clientID,
		sessionID:   sessionID,
		params:      frozen,
		digest:      digestValues(frozen),
		scope:       frozen.Get("scope"),
		redirectURI: frozen.Get("redirect_uri"),
		expiresAt:   s.now().Add(s.ttl),
	}
	uri, err := newRequestURI()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.entries[uri] = obj
	s.mu.Unlock()
	return uri, nil
}

// Redeem 按 request_uri 一次性兑换请求对象。
//
// 兑换要求：同一 client、同一会话、参数摘要一致、scope 与
// redirect_uri 与原请求一致、未过期。仅在校验全部通过时才消费
// 请求对象；整个校验与消费在同一把锁内原子完成，因此并发或
// 重复兑换只有一个能成功。校验失败不销毁对象，避免泄露的
// request_uri 被用于拒绝服务。
func (s *Store) Redeem(clientID, sessionID, requestURI string, params url.Values) (*RequestObject, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	obj, ok := s.entries[requestURI]
	if !ok {
		return nil, ErrRequestNotFound
	}
	if !s.now().Before(obj.expiresAt) {
		// 过期对象立即清除。
		delete(s.entries, requestURI)
		return nil, ErrRequestExpired
	}
	if clientID != obj.clientID {
		return nil, ErrClientMismatch
	}
	if sessionID != obj.sessionID {
		return nil, ErrSessionMismatch
	}
	if digestValues(params) != obj.digest {
		// 参数摘要不一致时进一步区分 scope / redirect_uri 篡改，
		// 便于授权服务器记录与告警。
		if params.Get("scope") != obj.scope {
			return nil, ErrScopeMismatch
		}
		if params.Get("redirect_uri") != obj.redirectURI {
			return nil, ErrRedirectMismatch
		}
		return nil, ErrParamMismatch
	}
	// 全部校验通过：一次性消费。
	delete(s.entries, requestURI)
	return obj, nil
}

// newRequestURI 生成不可猜测的 request_uri。
func newRequestURI() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("par: generate request_uri: %w", err)
	}
	return RequestURIScheme + base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// cloneValues 深拷贝参数，保证入库快照与外部修改隔离。
func cloneValues(v url.Values) url.Values {
	out := make(url.Values, len(v))
	for k, vals := range v {
		cp := make([]string, len(vals))
		copy(cp, vals)
		out[k] = cp
	}
	return out
}

// digestValues 计算参数的规范摘要：键排序、值排序后拼接编码，
// 使同一组参数无论提交顺序如何都得到相同摘要。
func digestValues(v url.Values) string {
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for _, k := range keys {
		vals := append([]string(nil), v[k]...)
		sort.Strings(vals)
		for _, val := range vals {
			sb.WriteString(url.QueryEscape(k))
			sb.WriteByte('=')
			sb.WriteString(url.QueryEscape(val))
			sb.WriteByte('&')
		}
	}
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}
