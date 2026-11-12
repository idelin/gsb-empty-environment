// Package peerverify 对 XPC 对端做与审计令牌绑定的代码签名校验和授权判定。
//
// 安全模型：
//
//   - 守护进程绝不能仅凭对端 pid 缓存“已通过检查”的结论。pid 是可复用的，
//     且对端可在通过检查后 exec 另一个二进制。放行结论以完整审计令牌
//     （pid、pidversion、euid）为键缓存，pidversion 或 euid 任一变化都会
//     得到不同的键，从而强制重新判定。
//   - exec 与退出事件通过 OnExec/OnExit 主动作废对应审计令牌（以及同一 pid
//     索引到的旧令牌）的缓存结论，避免同 pidversion 语义下的陈旧结果。
//   - 登记项以（团队 ID，签名标识）二元组为键，二者必须同时匹配；登记项可
//     额外要求对端为平台二进制。
//   - 授权键不与连接绑定、不在连接建立时缓存。每次 Authorize 都按当前审计
//     令牌对应的签名身份在最新登记表中实时查询授权键，因此登记项的运行期
//     变更会立即生效。
//
// 生产环境中 Evaluator 的实现应使用 SecCodeCopyGuestWithAttributes 配合
// kSecGuestAttributeAudit（由 xpc_connection_get_audit_token 取得），再通过
// SecCodeCheckValidityWithErrors 和 SecCodeCopySigningInformation 得到
// CodeIdentity，而不是自行解析 Mach-O。
package peerverify

import (
	"errors"
	"fmt"
	"sync"
)

// AuditToken 是 XPC 连接对端的审计令牌。三个字段共同标识一个进程实例，
// 任何一个字段不同都视为不同的对端，必须独立判定。
type AuditToken struct {
	PID        int32
	PIDVersion uint32
	EUID       uint32
}

// CodeIdentity 是对端代码要求（designated requirement）的判定结果。
type CodeIdentity struct {
	// TeamID 为签名中的团队 ID；平台二进制可能为空字符串。
	TeamID string
	// SigningID 为签名标识（identifier / signing identifier）。
	SigningID string
	// PlatformBinary 表示对端是否为 Apple 平台二进制。
	PlatformBinary bool
}

// Evaluator 依据审计令牌对当前对端做代码要求判定。实现必须对当前存活的
// 进程实例判定，且不得在内部按 pid 缓存结论。
type Evaluator interface {
	EvaluateCode(token AuditToken) (CodeIdentity, error)
}

// Registration 是一个受信对端登记项及其可使用的授权键集合。
type Registration struct {
	// TeamID 与 SigningID 必须与判定结果同时相等才算匹配。
	TeamID    string
	SigningID string
	// RequirePlatformBinary 为 true 时，还要求对端是平台二进制。
	RequirePlatformBinary bool
	// AllowedKeys 为该身份允许调用的方法授权键。
	AllowedKeys []string
}

// 常见判定失败，调用方可用 errors.Is 区分拒绝原因。
var (
	// ErrNotRegistered 表示签名身份没有匹配的登记项（团队 ID、签名标识
	// 或平台二进制要求不一致）。
	ErrNotRegistered = errors.New("peerverify: signing identity is not registered")
	// ErrUnauthorizedKey 表示身份合法但请求的授权键未授予该身份。
	ErrUnauthorizedKey = errors.New("peerverify: authorization key not granted to identity")
)

// EvaluatorError 包装底层代码要求判定器返回的错误。失败结果不会被缓存。
type EvaluatorError struct {
	Token AuditToken
	Err   error
}

func (e *EvaluatorError) Error() string {
	return fmt.Sprintf("peerverify: code requirement evaluation failed for %+v: %v", e.Token, e.Err)
}

func (e *EvaluatorError) Unwrap() error { return e.Err }

type identityKey struct {
	teamID    string
	signingID string
}

type registered struct {
	requirePlatform bool
	keys            map[string]struct{}
}

type decision struct {
	identity CodeIdentity
}

// Verifier 是并发安全的对端校验器，供连接建立和每条消息处理路径共用。
type Verifier struct {
	evaluator Evaluator

	mu            sync.RWMutex
	registrations map[identityKey]registered
	// decisions 以完整审计令牌为键，绝不以 pid 为键。
	decisions map[AuditToken]decision
	// pids 维护 pid 到最近一个受信审计令牌的索引，供 exec/退出事件作废旧结果。
	pids map[int32]AuditToken
}

// New 创建校验器。registrations 为初始登记项，后续可用 UpsertRegistration
// 在运行期变更。
func New(evaluator Evaluator, registrations ...Registration) (*Verifier, error) {
	if evaluator == nil {
		return nil, errors.New("peerverify: evaluator is required")
	}
	v := &Verifier{
		evaluator:     evaluator,
		registrations: make(map[identityKey]registered),
		decisions:     make(map[AuditToken]decision),
		pids:          make(map[int32]AuditToken),
	}
	for _, reg := range registrations {
		if err := v.UpsertRegistration(reg); err != nil {
			return nil, err
		}
	}
	return v, nil
}

// UpsertRegistration 新增或替换一个登记项。团队 ID 与签名标识共同构成键。
func (v *Verifier) UpsertRegistration(reg Registration) error {
	if reg.SigningID == "" {
		return errors.New("peerverify: registration requires a signing id")
	}
	keys := make(map[string]struct{}, len(reg.AllowedKeys))
	for _, key := range reg.AllowedKeys {
		if key == "" {
			return errors.New("peerverify: authorization key must not be empty")
		}
		keys[key] = struct{}{}
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.registrations[identityKey{teamID: reg.TeamID, signingID: reg.SigningID}] = registered{
		requirePlatform: reg.RequirePlatformBinary,
		keys:            keys,
	}
	return nil
}

// RemoveRegistration 删除一个登记项；身份不存在时为空操作。
func (v *Verifier) RemoveRegistration(teamID, signingID string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.registrations, identityKey{teamID: teamID, signingID: signingID})
}

// Connect 在连接建立时调用：对审计令牌做一次完整判定并缓存通过结论。
// 连接被拒绝时返回包装了 ErrNotRegistered 的错误或 *EvaluatorError。
// 该方法是快速失败的便捷入口；每条消息处理仍须调用 Authorize。
func (v *Verifier) Connect(token AuditToken) (CodeIdentity, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.evaluateLocked(token)
}

// Authorize 在每条消息处理时调用。它确保当前审计令牌已通过判定（必要时
// 重新判定），并按该令牌当前对应的签名身份在最新登记表中实时核对授权键，
// 不使用连接建立时缓存的任何授权信息。
// requiredKeys 为空时只要求身份合法。
func (v *Verifier) Authorize(token AuditToken, requiredKeys ...string) error {
	v.mu.Lock()
	identity, err := v.evaluateLocked(token)
	if err != nil {
		v.mu.Unlock()
		return err
	}
	entry, ok := v.lookupLocked(identity)
	// 登记表在每次消息处理时实时查询，不在判定结果中缓存授权键。
	v.mu.Unlock()
	if !ok {
		return notRegisteredError(identity)
	}
	for _, key := range requiredKeys {
		if _, allowed := entry.keys[key]; !allowed {
			return fmt.Errorf("%w: %q not granted to team=%q signing=%q",
				ErrUnauthorizedKey, key, identity.TeamID, identity.SigningID)
		}
	}
	return nil
}

// OnExec 处理对端 exec 事件：作废该审计令牌以及同一 pid 下旧审计令牌的
// 缓存结论，使后续消息强制重新判定。
func (v *Verifier) OnExec(token AuditToken) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.invalidateLocked(token)
}

// OnExit 处理对端退出事件，语义与 OnExec 相同：同一 pid 之后被复用时，
// 新进程不会继承任何旧的放行结论。
func (v *Verifier) OnExit(token AuditToken) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.invalidateLocked(token)
}

// hasDecision 仅供本包测试使用：判断某审计令牌当前是否持有放行结论。
func (v *Verifier) hasDecision(token AuditToken) bool {
	v.mu.RLock()
	defer v.mu.RUnlock()
	_, ok := v.decisions[token]
	return ok
}

// evaluateLocked 调用方须持有 v.mu。判定器调用期间会临时释放锁，以免
// SecCode 等较慢的系统调用阻塞其他连接；返回前重新获取锁。
func (v *Verifier) evaluateLocked(token AuditToken) (CodeIdentity, error) {
	if cached, ok := v.decisions[token]; ok {
		return cached.identity, nil
	}

	v.mu.Unlock()
	identity, evalErr := v.evaluator.EvaluateCode(token)
	v.mu.Lock()

	if evalErr != nil {
		// 失败不缓存，允许调用方在条件变化后重试。
		return CodeIdentity{}, &EvaluatorError{Token: token, Err: evalErr}
	}
	// 释放锁期间可能已有其他 goroutine 完成同一令牌的判定。
	if cached, ok := v.decisions[token]; ok {
		return cached.identity, nil
	}
	_, ok := v.lookupLocked(identity)
	if !ok {
		// 未登记的身份不缓存，登记项更新后可立即重新判定。
		return identity, notRegisteredError(identity)
	}
	v.decisions[token] = decision{identity: identity}
	v.pids[token.PID] = token
	return identity, nil
}

// lookupLocked 要求团队 ID 与签名标识同时命中登记项，并满足平台二进制要求。
func (v *Verifier) lookupLocked(identity CodeIdentity) (registered, bool) {
	entry, ok := v.registrations[identityKey{
		teamID:    identity.TeamID,
		signingID: identity.SigningID,
	}]
	if !ok {
		return registered{}, false
	}
	if entry.requirePlatform && !identity.PlatformBinary {
		return registered{}, false
	}
	return entry, true
}

// invalidateLocked 作废给定审计令牌的结论，并清理同一 pid 索引到的旧令牌。
func (v *Verifier) invalidateLocked(token AuditToken) {
	delete(v.decisions, token)
	if indexed, ok := v.pids[token.PID]; ok {
		delete(v.decisions, indexed)
		delete(v.pids, token.PID)
	}
}

func notRegisteredError(identity CodeIdentity) error {
	return fmt.Errorf("%w: team=%q signing=%q platform=%t",
		ErrNotRegistered, identity.TeamID, identity.SigningID, identity.PlatformBinary)
}
