// Package peerauth 为基于 XPC 的权限代理提供对端连接校验与逐消息授权。
//
// 安全模型：
//   - 任何放行判定都绑定对端的完整审计令牌（pid + pidversion + euid），
//     绝不单独以 pid 作为索引，因此检查后 exec、pid 复用、euid 变化都会
//     导致缓存未命中并触发重新判定，旧令牌上的判定无法被借用。
//   - 代码签名身份（团队 ID、签名标识、是否平台二进制）由调用方注入的
//     IdentityEvaluator 在判定时实时求值，不接受连接建立时缓存的身份。
//   - 授权键按当前审计令牌对应到的签名身份查询登记表，团队 ID 与签名
//     标识（以及平台二进制标记）必须与登记项同时匹配，缺一不可。
//   - exec 与退出事件用于主动清除缓存，属于 pidversion 机制之外的纵深防御。
package peerauth

import (
	"errors"
	"fmt"
	"sync"
)

var (
	// ErrPeerNotRegistered 表示对端当前的签名身份在登记表中没有匹配项。
	ErrPeerNotRegistered = errors.New("peerauth: peer code-signature identity is not registered")
	// ErrUnauthorized 表示身份已登记，但请求的授权键不在其授权范围内。
	ErrUnauthorized = errors.New("peerauth: authorization key is not granted to the peer identity")
	// ErrInvalidGrant 表示 NewValidator 收到的登记项不合法。
	ErrInvalidGrant = errors.New("peerauth: invalid grant registration")
)

// AuditToken 是 XPC 连接对端的内核审计令牌。
// 三个字段共同参与身份比较，Pid 本身永远不代表一个可信身份。
type AuditToken struct {
	Pid        int
	PidVersion int
	Euid       int
}

// String 返回便于日志记录的令牌描述。
func (t AuditToken) String() string {
	return fmt.Sprintf("audit-token(pid=%d,pidversion=%d,euid=%d)", t.Pid, t.PidVersion, t.Euid)
}

// Identity 是对端进程当前的代码签名身份，由代码要求判定产生。
type Identity struct {
	TeamID         string
	SigningID      string
	PlatformBinary bool
}

// String 返回便于日志记录的身份描述。
func (id Identity) String() string {
	return fmt.Sprintf("identity(team=%q,signing-id=%q,platform=%t)", id.TeamID, id.SigningID, id.PlatformBinary)
}

// IdentityEvaluator 在调用时刻对给定审计令牌执行代码要求判定，
// 返回该令牌对应进程的当前签名身份。实现通常封装 SecCode 校验逻辑；
// 进程已不存在或校验失败时必须返回错误，不得返回缓存结果。
type IdentityEvaluator func(token AuditToken) (Identity, error)

// Grant 是一条签名身份登记项及其可调用的授权键集合。
// 平台二进制通常没有团队 ID，此时 TeamID 留空并将 PlatformBinary 置为 true。
type Grant struct {
	TeamID         string
	SigningID      string
	PlatformBinary bool
	AuthKeys       []string
}

type grantKey struct {
	teamID         string
	signingID      string
	platformBinary bool
}

type grantRecord struct {
	authKeys map[string]struct{}
}

// Validator 是并发安全的对端校验器，供连接建立回调与每条消息的处理
// 回调共同使用。所有内部状态均由互斥锁保护。
type Validator struct {
	mu       sync.RWMutex
	evaluate IdentityEvaluator
	grants   map[grantKey]grantRecord
	// cache 以完整审计令牌为键保存判定时观察到的签名身份。
	cache map[AuditToken]Identity
}

// NewValidator 用代码要求判定器和身份登记项构造校验器。
// 登记项的 SigningID 不可为空、AuthKeys 至少包含一个键，且不得重复登记。
func NewValidator(evaluator IdentityEvaluator, grants ...Grant) (*Validator, error) {
	if evaluator == nil {
		return nil, fmt.Errorf("%w: identity evaluator is nil", ErrInvalidGrant)
	}
	registered := make(map[grantKey]grantRecord, len(grants))
	for _, grant := range grants {
		if grant.SigningID == "" {
			return nil, fmt.Errorf("%w: signing id is required", ErrInvalidGrant)
		}
		if len(grant.AuthKeys) == 0 {
			return nil, fmt.Errorf("%w: at least one auth key is required for %q", ErrInvalidGrant, grant.SigningID)
		}
		key := grantKey{
			teamID:         grant.TeamID,
			signingID:      grant.SigningID,
			platformBinary: grant.PlatformBinary,
		}
		if _, exists := registered[key]; exists {
			return nil, fmt.Errorf("%w: duplicate grant for team=%q signing-id=%q platform=%t",
				ErrInvalidGrant, grant.TeamID, grant.SigningID, grant.PlatformBinary)
		}
		keys := make(map[string]struct{}, len(grant.AuthKeys))
		for _, authKey := range grant.AuthKeys {
			if authKey == "" {
				return nil, fmt.Errorf("%w: empty auth key in grant %q", ErrInvalidGrant, grant.SigningID)
			}
			keys[authKey] = struct{}{}
		}
		registered[key] = grantRecord{authKeys: keys}
	}
	return &Validator{
		evaluate: evaluator,
		grants:   registered,
		cache:    make(map[AuditToken]Identity),
	}, nil
}

// ValidateConnection 在 XPC 连接建立时调用：实时判定对端身份，确认其
// 在登记表中，并把身份绑定到完整审计令牌缓存起来。
func (v *Validator) ValidateConnection(token AuditToken) error {
	identity, err := v.identityFor(token)
	if err != nil {
		return fmt.Errorf("peerauth: evaluate %s: %w", token, err)
	}
	if _, ok := v.lookupGrant(identity); !ok {
		return fmt.Errorf("%w: %s %s", ErrPeerNotRegistered, token, identity)
	}
	return nil
}

// Authorize 在处理每条消息时调用：基于消息携带的当前审计令牌取得签名
// 身份，再按该身份查询登记项是否授予 authKey。未经过 ValidateConnection
// 的令牌会在此处按需实时判定；pidversion 或 euid 不同的令牌必然重新判定。
func (v *Validator) Authorize(token AuditToken, authKey string) error {
	identity, err := v.identityFor(token)
	if err != nil {
		return fmt.Errorf("peerauth: evaluate %s: %w", token, err)
	}
	record, ok := v.lookupGrant(identity)
	if !ok {
		return fmt.Errorf("%w: %s %s", ErrPeerNotRegistered, token, identity)
	}
	if _, allowed := record.authKeys[authKey]; !allowed {
		return fmt.Errorf("%w: %s %s key=%q", ErrUnauthorized, token, identity, authKey)
	}
	return nil
}

// NotifyExec 在收到对端进程的 exec 事件时调用，清除该 pid 上所有旧
// 审计令牌的缓存。exec 后内核给出的新 pidversion 本身也无法命中旧缓存，
// 事件清除用于及时释放状态并提供纵深防御。
func (v *Validator) NotifyExec(pid int) {
	v.dropPid(pid)
}

// NotifyExit 在收到对端进程退出事件时调用，清除该 pid 的全部缓存，
// 使随后复用同一 pid 的新进程不可能借用任何旧判定。
func (v *Validator) NotifyExit(pid int) {
	v.dropPid(pid)
}

// Invalidate 清除指定完整审计令牌的缓存，下一次校验将重新判定。
func (v *Validator) Invalidate(token AuditToken) {
	v.mu.Lock()
	delete(v.cache, token)
	v.mu.Unlock()
}

// identityFor 返回令牌当前对应的签名身份：完整令牌命中缓存时直接复用，
// 否则实时执行代码要求判定并缓存结果。外部判定在锁外执行，避免长时间持锁。
func (v *Validator) identityFor(token AuditToken) (Identity, error) {
	v.mu.RLock()
	identity, ok := v.cache[token]
	v.mu.RUnlock()
	if ok {
		return identity, nil
	}

	evaluated, err := v.evaluate(token)
	if err != nil {
		return Identity{}, err
	}

	v.mu.Lock()
	defer v.mu.Unlock()
	if cached, ok := v.cache[token]; ok {
		return cached, nil
	}
	v.cache[token] = evaluated
	return evaluated, nil
}

func (v *Validator) lookupGrant(identity Identity) (grantRecord, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	record, ok := v.grants[grantKey{
		teamID:         identity.TeamID,
		signingID:      identity.SigningID,
		platformBinary: identity.PlatformBinary,
	}]
	return record, ok
}

func (v *Validator) dropPid(pid int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	for token := range v.cache {
		if token.Pid == pid {
			delete(v.cache, token)
		}
	}
}
