package netsec

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// Permission 是绑定在 pinUvAuthToken 上的权限位。
type Permission uint8

const (
	PermMC   Permission = 1 << iota // makeCredential
	PermGA                          // getAssertion
	PermCM                          // credentialManagement
	PermBE                          // bioEnrollment
	PermLBW                         // largeBlobWrite
	PermACFG                        // authenticatorConfig
)

var (
	ErrTokenNotFound      = errors.New("token not found")
	ErrTokenExpired       = errors.New("token expired")
	ErrPermissionDenied   = errors.New("permission not granted on token")
	ErrPermissionConsumed = errors.New("mc permission already consumed")
	ErrRpIDMismatch       = errors.New("token rpId does not match request")
	ErrRpIDRequired       = errors.New("permission requires an rpId")
	ErrRpIDForbidden      = errors.New("permission forbids an rpId")
)

// Token 表示一枚已签发的 pinUvAuthToken 及其绑定的权限范围。
type Token struct {
	ID          string
	Permissions Permission
	RpID        string // 空串表示未绑定 rpId
	IssuedAt    time.Time
	LastUsedAt  time.Time
	mcConsumed  bool
}

// TokenManager 供 CTAP2 请求分发层调用，负责令牌的签发、校验与生命周期。
type TokenManager struct {
	mu      sync.Mutex
	tokens  map[string]*Token
	timeout time.Duration // 无操作期限
	now     func() time.Time
	nextID  uint64
}

// NewTokenManager 创建令牌管理器；idleTimeout 为无操作期限，超过即失效。
func NewTokenManager(idleTimeout time.Duration) *TokenManager {
	return &TokenManager{
		tokens:  make(map[string]*Token),
		timeout: idleTimeout,
		now:     time.Now,
	}
}

// Issue 按权限位与可选 rpId 签发令牌，并强制绑定规则：
// mc、ga 必须携带 rpId；cm 不得携带 rpId。
func (m *TokenManager) Issue(perms Permission, rpID string) (*Token, error) {
	if perms&(PermMC|PermGA) != 0 && rpID == "" {
		return nil, fmt.Errorf("%w: mc/ga", ErrRpIDRequired)
	}
	if perms&PermCM != 0 && rpID != "" {
		return nil, fmt.Errorf("%w: cm", ErrRpIDForbidden)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	now := m.now()
	tok := &Token{
		ID:          fmt.Sprintf("token-%d", m.nextID),
		Permissions: perms,
		RpID:        rpID,
		IssuedAt:    now,
		LastUsedAt:  now,
	}
	m.tokens[tok.ID] = tok
	return tok, nil
}

// Validate 在分发 CTAP2 请求前校验令牌：权限位必须存在，绑定的 rpId
// 必须与请求一致，且令牌未超过无操作期限。校验成功会刷新活跃时间。
func (m *TokenManager) Validate(tokenID string, perm Permission, rpID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	tok, ok := m.tokens[tokenID]
	if !ok {
		return ErrTokenNotFound
	}
	if err := m.checkLocked(tok); err != nil {
		return err
	}
	if tok.Permissions&perm != perm {
		return fmt.Errorf("%w: 0x%02x", ErrPermissionDenied, byte(perm))
	}
	if tok.RpID != "" && tok.RpID != rpID {
		return ErrRpIDMismatch
	}
	tok.LastUsedAt = m.now()
	return nil
}

// ConsumeMC 在一次成功的 makeCredential 之后调用，收回令牌的 mc 权限。
// 调用前同样执行完整校验，确保本次 makeCredential 本身合法。
func (m *TokenManager) ConsumeMC(tokenID string, rpID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	tok, ok := m.tokens[tokenID]
	if !ok {
		return ErrTokenNotFound
	}
	if err := m.checkLocked(tok); err != nil {
		return err
	}
	if tok.mcConsumed {
		return ErrPermissionConsumed
	}
	if tok.Permissions&PermMC == 0 {
		return fmt.Errorf("%w: mc", ErrPermissionDenied)
	}
	if tok.RpID != rpID {
		return ErrRpIDMismatch
	}
	tok.mcConsumed = true
	tok.Permissions &^= PermMC
	tok.LastUsedAt = m.now()
	return nil
}

// PowerCycle 模拟认证器重新上电：所有已签发令牌立即失效。
func (m *TokenManager) PowerCycle() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokens = make(map[string]*Token)
}

// checkLocked 校验过期（调用方须持锁）。过期令牌会被移除。
func (m *TokenManager) checkLocked(tok *Token) error {
	if m.now().Sub(tok.LastUsedAt) > m.timeout {
		delete(m.tokens, tok.ID)
		return ErrTokenExpired
	}
	return nil
}
