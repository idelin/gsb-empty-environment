package netsec

import (
	"errors"
)

var (
	// ErrEmptyTenant 表示签发或恢复时未提供租户标识。
	ErrEmptyTenant = errors.New("netsec: empty tenant")
	// ErrPolicyUnavailable 表示宿主未返回当前租户策略。
	ErrPolicyUnavailable = errors.New("netsec: current policy unavailable")
	// ErrTenantMismatch 表示票据租户与恢复请求租户不一致（跨租户使用）。
	ErrTenantMismatch = errors.New("netsec: ticket tenant does not match request tenant")
	// ErrPolicyRejectsLevel 表示当前策略不再允许票据记载的认证等级或协议选项。
	ErrPolicyRejectsLevel = errors.New("netsec: current policy rejects ticket auth level or protocol options")
	// ErrTicketExpired 表示票据已超过签发策略给定的有效期。
	ErrTicketExpired = errors.New("netsec: ticket expired")
)

// PolicyProvider 由接入宿主实现，按租户返回当前生效策略。
type PolicyProvider interface {
	PolicyForTenant(tenant string) (Policy, bool)
}

// PolicyProviderFunc 让普通函数满足 PolicyProvider。
type PolicyProviderFunc func(tenant string) (Policy, bool)

// PolicyForTenant 调用底层函数。
func (f PolicyProviderFunc) PolicyForTenant(tenant string) (Policy, bool) {
	return f(tenant)
}

// SessionRequest 是票据恢复成功后交给宿主重建会话的信息。
type SessionRequest struct {
	Tenant      string
	AuthLevel   AuthLevel
	Options     ProtocolOptions
	SessionData []byte
}

// Session 是宿主根据 SessionRequest 重建出的会话句柄。
type Session struct {
	Tenant      string
	AuthLevel   AuthLevel
	Options     ProtocolOptions
	SessionData []byte
	// PolicyChanged 表示票据在旧策略下签发，但当前策略仍允许其认证等级；
	// 宿主可据此决定是否需要重新协商或刷新票据。
	PolicyChanged bool
}

// SessionFactory 由宿主实现，只有全部安全检查通过后才会被调用。
type SessionFactory func(req SessionRequest) (*Session, error)

// Manager 封装票据签发与恢复，是接入宿主的主要入口。
type Manager struct {
	keyring *Keyring
	policy  PolicyProvider
	factory SessionFactory
	now     func() uint64
}

// Option 配置 Manager 的可选行为。
type Option func(*Manager)

// WithClock 注入时钟函数，返回 Unix 秒；主要用于测试宽限期与过期逻辑。
func WithClock(now func() uint64) Option {
	return func(m *Manager) {
		m.now = now
	}
}

// NewManager 创建恢复票据管理器。factory 为 nil 时使用默认工厂，
// 默认工厂仅透传票据中的会话数据，便于宿主快速接入。
func NewManager(keyring *Keyring, provider PolicyProvider, factory SessionFactory, opts ...Option) (*Manager, error) {
	if keyring == nil {
		return nil, errors.New("netsec: nil keyring")
	}
	if provider == nil {
		return nil, errors.New("netsec: nil policy provider")
	}
	if factory == nil {
		factory = defaultSessionFactory
	}
	m := &Manager{
		keyring: keyring,
		policy:  provider,
		factory: factory,
		now:     unixNow,
	}
	for _, opt := range opts {
		opt(m)
	}
	keyring.now = m.now
	return m, nil
}

func defaultSessionFactory(req SessionRequest) (*Session, error) {
	return &Session{
		Tenant:      req.Tenant,
		AuthLevel:   req.AuthLevel,
		Options:     req.Options,
		SessionData: append([]byte(nil), req.SessionData...),
	}, nil
}

// Issue 在当前策略下为已认证会话签发恢复票据。
//
// 认证等级和协议选项必须先被当前策略允许，否则拒绝签发。
func (m *Manager) Issue(tenant string, level AuthLevel, options ProtocolOptions, sessionData []byte) ([]byte, error) {
	if tenant == "" {
		return nil, ErrEmptyTenant
	}
	policy, ok := m.policy.PolicyForTenant(tenant)
	if !ok || policy.Tenant != tenant || policy.TicketTTL == 0 {
		return nil, ErrPolicyUnavailable
	}
	if !policy.Allows(level, options) {
		return nil, ErrPolicyRejectsLevel
	}

	now := m.now()
	claim := claims{
		tenant:       tenant,
		authLevel:    level,
		minVersion:   options.MinVersion,
		maxVersion:   options.MaxVersion,
		alpn:         append([]string(nil), options.ALPN...),
		policyDigest: policy.Digest(),
		issuedAt:     now,
		expiresAt:    now + policy.TicketTTL,
		sessionData:  append([]byte(nil), sessionData...),
	}
	plaintext, err := claim.marshal()
	if err != nil {
		return nil, err
	}
	return m.keyring.Seal(plaintext, additionalData(tenant, m.keyring.active.id))
}

// Resume 验证恢复票据并在全部检查通过后让宿主工厂重建会话。
//
// 校验顺序：密文认证 → 租户绑定 → 有效期 → 当前策略允许票据记载的
// 认证等级与协议选项。任何一步失败都返回错误且不会调用会话工厂。
func (m *Manager) Resume(tenant string, ticket []byte) (*Session, error) {
	if tenant == "" {
		return nil, ErrEmptyTenant
	}

	// 信封中的 key id 仅用于选择解密密钥；解密后还会显式核对租户。
	keyID, err := envelopeKeyID(ticket)
	if err != nil {
		return nil, err
	}
	plaintext, err := m.keyring.Open(ticket, additionalData(tenant, keyID))
	if err != nil {
		return nil, err
	}
	claim, err := unmarshalClaims(plaintext)
	if err != nil {
		return nil, err
	}

	// 防御纵深：即使 AAD 已绑定租户，再次显式比较，杜绝跨租户恢复。
	if claim.tenant != tenant {
		return nil, ErrTenantMismatch
	}

	now := m.now()
	if now >= claim.expiresAt {
		return nil, ErrTicketExpired
	}

	policy, ok := m.policy.PolicyForTenant(tenant)
	if !ok || policy.Tenant != tenant || policy.TicketTTL == 0 {
		return nil, ErrPolicyUnavailable
	}

	// 核心安全检查：先比较当前策略是否仍允许票据记载的等级和协议选项，
	// 而不是仅凭密文验证通过就恢复。
	options := ProtocolOptions{
		MinVersion: claim.minVersion,
		MaxVersion: claim.maxVersion,
		ALPN:       claim.alpn,
	}
	if !policy.Allows(claim.authLevel, options) {
		return nil, ErrPolicyRejectsLevel
	}

	session, err := m.factory(SessionRequest{
		Tenant:      tenant,
		AuthLevel:   claim.authLevel,
		Options:     options,
		SessionData: claim.sessionData,
	})
	if err != nil {
		return nil, err
	}
	session.PolicyChanged = claim.policyDigest != policy.Digest()
	return session, nil
}

// envelopeKeyID 从票据信封中读取密钥标识，不做任何解密。
func envelopeKeyID(ticket []byte) ([keyIDSize]byte, error) {
	var id [keyIDSize]byte
	if len(ticket) < 1+keyIDSize {
		return id, ErrMalformedTicket
	}
	if ticket[0] != envelopeVersion {
		return id, ErrMalformedTicket
	}
	copy(id[:], ticket[1:1+keyIDSize])
	return id, nil
}
