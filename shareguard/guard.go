// Package shareguard 实现安全聚合（Secure Aggregation）客户端的份额披露守卫。
//
// 它不保存任何秘密材料，只充当策略引擎：在协议各阶段记录可见的存活集合、
// 校验服务器给出的存活集合与一致性签名，并在解密阶段决定客户端是否允许
// 披露某个用户的成对种子份额（pairwise seed share）或自掩码份额
// （self mask share），从而抵御恶意服务器对同一用户先谎称掉线、后谎称在线、
// 凑齐两类份额还原个人输入的攻击。
//
// 守卫按顺序经过五个阶段：
//
//	PhaseBroadcastKey  广播密钥
//	PhaseShare         分享
//	PhaseMaskedInput   掩码输入（在此记录存活集合 U2）
//	PhaseConsistency   一致性签名（在此校验并锁定存活集合 U3）
//	PhaseDecryption    解密（在此最多接受一次披露请求）
//
// 任何违反策略的解密阶段操作都会终止会话；会话终止后守卫拒绝一切后续披露。
package shareguard

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Phase 标识安全聚合协议所处的阶段。阶段只能单调向前推进。
type Phase int

const (
	// PhaseBroadcastKey 是广播密钥阶段（初始阶段）。
	PhaseBroadcastKey Phase = iota
	// PhaseShare 是秘密分享阶段。
	PhaseShare
	// PhaseMaskedInput 是掩码输入阶段，客户端在此阶段记录存活集合 U2。
	PhaseMaskedInput
	// PhaseConsistency 是一致性签名阶段，客户端在此校验存活集合 U3 及签名。
	PhaseConsistency
	// PhaseDecryption 是解密阶段，客户端在此按策略披露份额。
	PhaseDecryption
)

// String 返回阶段的可读名称。
func (p Phase) String() string {
	switch p {
	case PhaseBroadcastKey:
		return "broadcast_key"
	case PhaseShare:
		return "share"
	case PhaseMaskedInput:
		return "masked_input"
	case PhaseConsistency:
		return "consistency"
	case PhaseDecryption:
		return "decryption"
	default:
		return fmt.Sprintf("unknown_phase(%d)", int(p))
	}
}

// Signature 是某个客户端对一致性阶段存活集合 U3 的签名。
type Signature struct {
	// Signer 是签名者的客户端标识，且必须属于被签名的存活集合 U3。
	Signer string
	// Value 是不透明签名值，由注入的 SignatureVerifier 解释与校验。
	Value []byte
}

// SignatureVerifier 校验某条存活集合签名是否有效。
//
// 实现方负责决定对存活集合的规范序列化（canonical encoding）方式，
// 这样守卫本身无需绑定具体的签名或编码方案（HMAC、Ed25519 等均可接入）。
type SignatureVerifier interface {
	// VerifyAliveSet 在签名 signer 对存活集合 aliveSet 有效时返回 nil。
	// aliveSet 由守卫预先排序去重，保证验证输入是确定的。
	VerifyAliveSet(signer string, aliveSet []string, signature []byte) error
}

// 守卫可能返回的哨兵错误，调用方使用 errors.Is 判定具体原因。
var (
	// ErrInvalidThreshold 表示构造守卫时给出的阈值 t 非法。
	ErrInvalidThreshold = errors.New("shareguard: threshold must be positive")
	// ErrInvalidPhase 表示在错误的阶段调用了某个方法，或阶段推进不合法。
	ErrInvalidPhase = errors.New("shareguard: operation not allowed in current phase")
	// ErrSessionTerminated 表示会话已终止，不再接受任何披露或状态变更。
	ErrSessionTerminated = errors.New("shareguard: session terminated")
	// ErrEmptyAliveSet 表示一致性阶段给出的存活集合 U3 为空。
	ErrEmptyAliveSet = errors.New("shareguard: alive set U3 is empty")
	// ErrAliveSetNotSubset 表示 U3 不是掩码输入阶段记录的 U2 的子集。
	ErrAliveSetNotSubset = errors.New("shareguard: U3 must be a subset of U2")
	// ErrBelowThreshold 表示存活集合大小或有效签名数量小于阈值 t。
	ErrBelowThreshold = errors.New("shareguard: alive set size or valid signatures below threshold")
	// ErrSignerOutsideAliveSet 表示某个签名者不在被签名的存活集合 U3 内。
	ErrSignerOutsideAliveSet = errors.New("shareguard: signer is not a member of U3")
	// ErrBadSignature 表示某条签名未通过验证。
	ErrBadSignature = errors.New("shareguard: invalid signature over U3")
	// ErrConsistencyNotAccepted 表示解密请求到达时尚未通过一致性校验。
	ErrConsistencyNotAccepted = errors.New("shareguard: consistency statement not accepted")
	// ErrDecryptionAlreadyRequested 表示一次会话内收到了第二次解密请求。
	ErrDecryptionAlreadyRequested = errors.New("shareguard: decryption request already answered once")
	// ErrSurvivingSetMismatch 表示请求中的存活集合 S 与已签名的 U3 不相等。
	ErrSurvivingSetMismatch = errors.New("shareguard: surviving set S must equal signed U3")
	// ErrDroppedSetMismatch 表示请求中的掉线集合 D 不等于 U2\U3。
	ErrDroppedSetMismatch = errors.New("shareguard: dropped set D must equal U2 minus U3")
	// ErrSetsIntersect 表示掉线集合 D 与存活集合 S 存在交集。
	ErrSetsIntersect = errors.New("shareguard: dropped set D and surviving set S must be disjoint")
	// ErrUserNotInRequest 表示请求披露的用户既不在 D 也不在 S 中。
	ErrUserNotInRequest = errors.New("shareguard: user is not part of this disclosure request")
	// ErrDoubleDisclosure 表示同一用户的另一类份额此前已经披露，拒绝凑齐两类份额。
	ErrDoubleDisclosure = errors.New("shareguard: the other share type for this user was already disclosed")
)

// shareKind 记录某个用户已经披露的份额类型。
type shareKind int

const (
	kindPairwise shareKind = iota + 1
	kindSelfMask
)

func (k shareKind) String() string {
	switch k {
	case kindPairwise:
		return "pairwise_seed_share"
	case kindSelfMask:
		return "self_mask_share"
	default:
		return "unknown"
	}
}

// Guard 是单个安全聚合会话的份额披露守卫。
//
// Guard 的所有方法均支持并发调用；任何解密阶段的策略违规都会终止会话，
// 此后 Terminated 返回 true 且所有披露方法都返回 ErrSessionTerminated。
type Guard struct {
	mu         sync.Mutex
	phase      Phase
	threshold  int
	verifier   SignatureVerifier
	terminated bool
	// terminateReason 保存首次终止原因，可通过 TerminationReason 读取。
	terminateReason error

	// u2 是掩码输入阶段实际观察到的存活集合。
	u2 map[string]struct{}
	// u3 是一致性阶段校验通过并锁定的存活集合。
	u3 map[string]struct{}

	consistencyAccepted bool

	// 一次解密请求只允许回应一次。
	decryptionRequested bool
	droppedSet          map[string]struct{}
	survivingSet        map[string]struct{}

	// disclosed 记录每个用户已披露的份额类型，用于跨类型互斥。
	disclosed map[string]shareKind
}

// NewGuard 以阈值 t 和签名验证器创建一个守卫，初始阶段为广播密钥。
// threshold 必须为正数，verifier 不能为 nil。
func NewGuard(threshold int, verifier SignatureVerifier) (*Guard, error) {
	if threshold <= 0 {
		return nil, ErrInvalidThreshold
	}
	if verifier == nil {
		return nil, errors.New("shareguard: signature verifier is required")
	}
	return &Guard{
		phase:     PhaseBroadcastKey,
		threshold: threshold,
		verifier:  verifier,
		u2:        make(map[string]struct{}),
		u3:        make(map[string]struct{}),
		disclosed: make(map[string]shareKind),
	}, nil
}

// Phase 返回守卫当前所处的阶段。
func (g *Guard) Phase() Phase {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.phase
}

// Terminated 报告会话是否已被终止。
func (g *Guard) Terminated() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.terminated
}

// TerminationReason 返回导致会话终止的首个错误；会话仍存活时返回 nil。
func (g *Guard) TerminationReason() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.terminateReason
}

// Threshold 返回构造时设定的阈值 t。
func (g *Guard) Threshold() int {
	return g.threshold
}

// AdvanceTo 将会话推进到目标阶段。阶段只能严格向前移动一步，
// 已终止的会话拒绝任何推进。
func (g *Guard) AdvanceTo(target Phase) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.terminated {
		return fmt.Errorf("%w: %v", ErrSessionTerminated, g.terminateReason)
	}
	if target != g.phase+1 || target > PhaseDecryption {
		return fmt.Errorf("%w: cannot move from %s to %s", ErrInvalidPhase, g.phase, target)
	}
	g.phase = target
	return nil
}

// ObserveParticipant 在掩码输入阶段记录一个存活客户端，构成存活集合 U2。
// 重复记录同一标识是幂等的。
func (g *Guard) ObserveParticipant(clientID string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.terminated {
		return fmt.Errorf("%w: %v", ErrSessionTerminated, g.terminateReason)
	}
	if g.phase != PhaseMaskedInput {
		return fmt.Errorf("%w: U2 can only be recorded in %s", ErrInvalidPhase, PhaseMaskedInput)
	}
	g.u2[clientID] = struct{}{}
	return nil
}

// ObservedAliveSet 返回掩码输入阶段记录的存活集合 U2 的有序副本。
func (g *Guard) ObservedAliveSet() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return sortedKeys(g.u2)
}

// AcceptConsistencyStatement 在一致性阶段校验服务器给出的存活集合 u3 及签名。
//
// 校验规则：
//   - u3 非空且是 U2 的子集；
//   - |u3| 不小于阈值 t；
//   - 每条签名的签名者都必须属于 u3，且签名对 u3 验证通过；
//   - 去重后的有效签名数量至少为 t。
//
// 一致性阶段的校验失败不会终止会话（协议允许服务器重发修正后的声明），
// 但只有校验通过后 U3 才会被锁定；调用方应把反复失败视作异常并主动 Terminate。
func (g *Guard) AcceptConsistencyStatement(u3 []string, signatures []Signature) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.terminated {
		return fmt.Errorf("%w: %v", ErrSessionTerminated, g.terminateReason)
	}
	if g.phase != PhaseConsistency {
		return fmt.Errorf("%w: U3 can only be accepted in %s", ErrInvalidPhase, PhaseConsistency)
	}
	if g.consistencyAccepted {
		return fmt.Errorf("%w: U3 already locked", ErrInvalidPhase)
	}

	candidate := toSet(u3)
	if len(candidate) == 0 {
		return ErrEmptyAliveSet
	}
	if !isSubset(candidate, g.u2) {
		return ErrAliveSetNotSubset
	}
	if len(candidate) < g.threshold {
		return fmt.Errorf("%w: |U3|=%d, t=%d", ErrBelowThreshold, len(candidate), g.threshold)
	}

	canonical := sortedKeys(candidate)
	validSigners := make(map[string]struct{})
	for _, sig := range signatures {
		if _, ok := candidate[sig.Signer]; !ok {
			return fmt.Errorf("%w: %q", ErrSignerOutsideAliveSet, sig.Signer)
		}
		if err := g.verifier.VerifyAliveSet(sig.Signer, canonical, sig.Value); err != nil {
			return fmt.Errorf("%w: signer %q: %v", ErrBadSignature, sig.Signer, err)
		}
		validSigners[sig.Signer] = struct{}{}
	}
	if len(validSigners) < g.threshold {
		return fmt.Errorf("%w: valid signatures=%d, t=%d", ErrBelowThreshold, len(validSigners), g.threshold)
	}

	g.u3 = candidate
	g.consistencyAccepted = true
	return nil
}

// SignedAliveSet 返回一致性阶段锁定的存活集合 U3 的有序副本；
// 尚未通过一致性校验时返回 nil。
func (g *Guard) SignedAliveSet() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.consistencyAccepted {
		return nil
	}
	return sortedKeys(g.u3)
}

// AcceptDecryptionRequest 在解密阶段接受唯一一次披露请求。
//
// dropped D 是请求披露成对种子份额的掉线用户，surviving S 是请求披露
// 自掩码份额的存活用户。守卫要求：
//   - 会话尚未回应过解密请求（一次会话只回应一次）；
//   - S 与已签名的 U3 完全相等；
//   - D 与 U2\U3 完全相等；
//   - D 与 S 互不相交。
//
// 任一条件不满足都会终止会话，因为这表明服务器在试图操纵披露集合。
func (g *Guard) AcceptDecryptionRequest(dropped, surviving []string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.terminated {
		return fmt.Errorf("%w: %v", ErrSessionTerminated, g.terminateReason)
	}
	if g.phase != PhaseDecryption {
		return g.terminateLocked(fmt.Errorf("%w: disclosure requests are only accepted in %s", ErrInvalidPhase, PhaseDecryption))
	}
	if !g.consistencyAccepted {
		return g.terminateLocked(ErrConsistencyNotAccepted)
	}
	if g.decryptionRequested {
		return g.terminateLocked(ErrDecryptionAlreadyRequested)
	}

	dSet := toSet(dropped)
	sSet := toSet(surviving)

	if !setEquals(sSet, g.u3) {
		return g.terminateLocked(ErrSurvivingSetMismatch)
	}
	expectedD := setDifference(g.u2, g.u3)
	if !setEquals(dSet, expectedD) {
		return g.terminateLocked(ErrDroppedSetMismatch)
	}
	if intersects(dSet, sSet) {
		return g.terminateLocked(ErrSetsIntersect)
	}

	g.decryptionRequested = true
	g.droppedSet = dSet
	g.survivingSet = sSet
	return nil
}

// DisclosePairwise 尝试授权披露 user 的成对种子份额。
//
// user 必须属于本次请求的掉线集合 D。若该用户的自掩码份额此前已经披露，
// 守卫拒绝并终止会话；重复请求同一类份额是幂等成功，便于客户端重试发送。
// 返回 nil 仅表示策略放行，真正的份额材料由调用方自行保管与发送。
func (g *Guard) DisclosePairwise(user string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.authorizeLocked(user, kindPairwise)
}

// DiscloseSelfMask 尝试授权披露 user 的自掩码份额。
//
// user 必须属于本次请求的存活集合 S（即已签名的 U3）。若该用户的成对
// 种子份额此前已经披露，守卫拒绝并终止会话；重复请求同一类份额幂等成功。
func (g *Guard) DiscloseSelfMask(user string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.authorizeLocked(user, kindSelfMask)
}

// authorizeLocked 在持有锁的前提下执行份额互斥策略。
func (g *Guard) authorizeLocked(user string, kind shareKind) error {
	if g.terminated {
		return fmt.Errorf("%w: %v", ErrSessionTerminated, g.terminateReason)
	}
	if g.phase != PhaseDecryption || !g.decryptionRequested {
		return g.terminateLocked(fmt.Errorf("%w: no accepted decryption request", ErrInvalidPhase))
	}
	// 互斥优先：只要该用户另一类份额已经披露，无论本次请求把它放进
	// 哪个集合，都直接拒绝并终止，不给服务器通过操纵集合绕过的机会。
	if prior, ok := g.disclosed[user]; ok {
		if prior == kind {
			return nil
		}
		return g.terminateLocked(fmt.Errorf("%w: user %q already disclosed %s, refusing %s",
			ErrDoubleDisclosure, user, prior, kind))
	}

	var allowedSet map[string]struct{}
	if kind == kindPairwise {
		allowedSet = g.droppedSet
	} else {
		allowedSet = g.survivingSet
	}
	if _, ok := allowedSet[user]; !ok {
		return g.terminateLocked(fmt.Errorf("%w: %q", ErrUserNotInRequest, user))
	}

	g.disclosed[user] = kind
	return nil
}

// Terminate 以给定原因终止会话。终止后任何阶段推进、解密请求或份额披露
// 都会返回 ErrSessionTerminated。对已终止的会话调用是幂等的，且不会
// 覆盖首次终止原因；reason 为 nil 时使用通用原因。
func (g *Guard) Terminate(reason error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.terminated {
		return
	}
	if reason == nil {
		reason = errors.New("shareguard: terminated by caller")
	}
	g.terminateLocked(reason)
}

// terminateLocked 在已持有锁时终止会话并返回包装了终止原因的错误。
func (g *Guard) terminateLocked(reason error) error {
	g.terminated = true
	g.terminateReason = reason
	return fmt.Errorf("%w: %v", ErrSessionTerminated, reason)
}

// DroppedSet 返回本次解密请求中掉线集合 D 的有序副本；请求未接受时为 nil。
func (g *Guard) DroppedSet() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.decryptionRequested {
		return nil
	}
	return sortedKeys(g.droppedSet)
}

// SurvivingSet 返回本次解密请求中存活集合 S 的有序副本；请求未接受时为 nil。
func (g *Guard) SurvivingSet() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.decryptionRequested {
		return nil
	}
	return sortedKeys(g.survivingSet)
}

// DisclosedShareKind 返回某用户已披露的份额类型描述；尚未披露时返回空串。
// 返回值为 "pairwise_seed_share" 或 "self_mask_share"。
func (g *Guard) DisclosedShareKind(user string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if kind, ok := g.disclosed[user]; ok {
		return kind.String()
	}
	return ""
}

// 以下为守卫内部使用的集合工具，输入均为已持锁的 map，不做并发保护。

func toSet(ids []string) map[string]struct{} {
	set := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return set
}

func sortedKeys(set map[string]struct{}) []string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func isSubset(subset, superset map[string]struct{}) bool {
	for k := range subset {
		if _, ok := superset[k]; !ok {
			return false
		}
	}
	return true
}

func setEquals(a, b map[string]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	return isSubset(a, b)
}

func setDifference(a, b map[string]struct{}) map[string]struct{} {
	result := make(map[string]struct{})
	for k := range a {
		if _, ok := b[k]; !ok {
			result[k] = struct{}{}
		}
	}
	return result
}

func intersects(a, b map[string]struct{}) bool {
	// 在较小的集合上遍历，减少查找次数。
	small, large := a, b
	if len(b) < len(a) {
		small, large = b, a
	}
	for k := range small {
		if _, ok := large[k]; ok {
			return true
		}
	}
	return false
}
