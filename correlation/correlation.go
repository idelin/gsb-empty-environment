// Package correlation 为 SAML 服务提供方（SP）提供响应关联与重放防护。
//
// 发出 AuthnRequest 时通过 RegisterRequest 登记请求 ID 与过期时间；
// 收到 IdP 响应时用 Validate 校验：InResponseTo 必须命中一条未消费的
// 登记项且只能消费一次；无 InResponseTo 的 IdP 发起登录仅在配置允许时
// 接受；断言 ID 已见即拒绝；Recipient 必须等于 ACS 地址；NotOnOrAfter
// 允许 60 秒时钟偏差。
package correlation

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// DefaultClockSkew 是 NotOnOrAfter 校验允许的默认时钟偏差。
const DefaultClockSkew = 60 * time.Second

var (
	// ErrUnknownRequestID 表示 InResponseTo 未命中任何未消费的登记项
	// （包括 ID 错误、已被消费或从未登记）。
	ErrUnknownRequestID = errors.New("correlation: unknown or already consumed request ID")
	// ErrRequestExpired 表示命中的登记项已过期。
	ErrRequestExpired = errors.New("correlation: registered request has expired")
	// ErrIDPInitiatedNotAllowed 表示收到无 InResponseTo 的 IdP 发起登录，
	// 但配置未允许。
	ErrIDPInitiatedNotAllowed = errors.New("correlation: IdP-initiated login is not allowed")
	// ErrAssertionReplayed 表示断言 ID 已被消费过（重放）。
	ErrAssertionReplayed = errors.New("correlation: assertion ID already seen")
	// ErrRecipientMismatch 表示 Recipient 不等于本服务的 ACS 地址。
	ErrRecipientMismatch = errors.New("correlation: recipient does not match ACS URL")
	// ErrAssertionExpired 表示断言已超过 NotOnOrAfter（含时钟偏差）。
	ErrAssertionExpired = errors.New("correlation: assertion expired (NotOnOrAfter passed)")
)

// Config 是 Validator 的配置。
type Config struct {
	// ACSURL 是本服务的断言消费服务地址，响应的 Recipient 必须与之相等。
	ACSURL string
	// AllowIDPInitiated 为 true 时接受无 InResponseTo 的 IdP 发起登录。
	AllowIDPInitiated bool
	// ClockSkew 是 NotOnOrAfter 允许的时钟偏差，零值使用 DefaultClockSkew。
	ClockSkew time.Duration
	// Now 返回当前时间，主要用于测试；nil 时使用 time.Now。
	Now func() time.Time
}

// Response 携带校验一条 SAML 响应所需的关联字段。
type Response struct {
	// InResponseTo 是响应中 SubjectConfirmationData 的 InResponseTo 属性。
	// 为空表示 IdP 发起的登录。
	InResponseTo string
	// AssertionID 是断言的 ID 属性，用于重放检测。
	AssertionID string
	// Recipient 是 SubjectConfirmationData 的 Recipient 属性。
	Recipient string
	// NotOnOrAfter 是 SubjectConfirmationData 的 NotOnOrAfter 属性。
	NotOnOrAfter time.Time
}

// Validator 登记待完成的 AuthnRequest 并校验响应。并发安全。
type Validator struct {
	cfg Config

	mu sync.Mutex
	// pending 保存请求 ID 到过期时间的映射，命中后即删除（消费一次）。
	pending map[string]time.Time
	// seenAssertions 保存已见断言 ID 及其过期时间（用于清理）。
	seenAssertions map[string]time.Time
}

// New 用给定配置创建 Validator。
func New(cfg Config) *Validator {
	if cfg.ClockSkew == 0 {
		cfg.ClockSkew = DefaultClockSkew
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Validator{
		cfg:            cfg,
		pending:        make(map[string]time.Time),
		seenAssertions: make(map[string]time.Time),
	}
}

// RegisterRequest 在发出 AuthnRequest 时登记其 ID 与过期时间。
func (v *Validator) RegisterRequest(id string, expiresAt time.Time) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.pending[id] = expiresAt
}

// Validate 校验一条 SAML 响应的关联性与时效性。
//
// 校验顺序：InResponseTo 关联（命中即消费）→ IdP 发起策略 →
// 断言重放 → Recipient → NotOnOrAfter。任何一步失败都返回错误，
// 且已消费的登记项与已记录的断言 ID 不会回滚，避免校验顺序被探测利用。
func (v *Validator) Validate(resp Response) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	now := v.cfg.Now()

	if resp.InResponseTo != "" {
		expiresAt, ok := v.pending[resp.InResponseTo]
		if !ok {
			return fmt.Errorf("%w: %q", ErrUnknownRequestID, resp.InResponseTo)
		}
		// 无论后续校验成败，登记项一经命中即消费，防止同一
		// InResponseTo 被用于多次尝试。
		delete(v.pending, resp.InResponseTo)
		if !now.Before(expiresAt) {
			return fmt.Errorf("%w: %q", ErrRequestExpired, resp.InResponseTo)
		}
	} else if !v.cfg.AllowIDPInitiated {
		return ErrIDPInitiatedNotAllowed
	}

	// 关联判定完成后再清理过期项，保证过期的登记项能给出
	// ErrRequestExpired 而不是被提前清理成 ErrUnknownRequestID。
	v.pruneLocked(now)

	if resp.AssertionID == "" {
		return fmt.Errorf("%w: empty assertion ID", ErrAssertionReplayed)
	}
	if _, seen := v.seenAssertions[resp.AssertionID]; seen {
		return fmt.Errorf("%w: %q", ErrAssertionReplayed, resp.AssertionID)
	}
	v.seenAssertions[resp.AssertionID] = resp.NotOnOrAfter.Add(v.cfg.ClockSkew)

	if resp.Recipient != v.cfg.ACSURL {
		return fmt.Errorf("%w: got %q, want %q", ErrRecipientMismatch, resp.Recipient, v.cfg.ACSURL)
	}

	if !now.Before(resp.NotOnOrAfter.Add(v.cfg.ClockSkew)) {
		return fmt.Errorf("%w: NotOnOrAfter=%s", ErrAssertionExpired, resp.NotOnOrAfter.UTC())
	}

	return nil
}

// pruneLocked 清理过期的登记项与断言 ID 记录，调用方须持有锁。
func (v *Validator) pruneLocked(now time.Time) {
	for id, expiresAt := range v.pending {
		if !now.Before(expiresAt) {
			delete(v.pending, id)
		}
	}
	for id, expiresAt := range v.seenAssertions {
		if !now.Before(expiresAt) {
			delete(v.seenAssertions, id)
		}
	}
}
