package netsec

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Phase 标识安全聚合协议的运行阶段。阶段只能按声明顺序单向前进。
type Phase int

const (
	// PhaseBroadcastKeys 广播密钥阶段。
	PhaseBroadcastKeys Phase = iota
	// PhaseShareSeeds 秘密分享阶段。
	PhaseShareSeeds
	// PhaseMaskedInput 掩码输入阶段。
	PhaseMaskedInput
	// PhaseConsistencySign 一致性签名阶段。
	PhaseConsistencySign
	// PhaseDecrypt 解密（份额披露）阶段。
	PhaseDecrypt
)

// String 返回阶段的可读名称。
func (p Phase) String() string {
	switch p {
	case PhaseBroadcastKeys:
		return "broadcast-keys"
	case PhaseShareSeeds:
		return "share-seeds"
	case PhaseMaskedInput:
		return "masked-input"
	case PhaseConsistencySign:
		return "consistency-sign"
	case PhaseDecrypt:
		return "decrypt"
	default:
		return fmt.Sprintf("unknown-phase(%d)", int(p))
	}
}

// ShareKind 表示一次披露涉及的份额类型。
type ShareKind int

const (
	// SharePairwiseSeed 成对种子份额，只允许对掉线用户披露。
	SharePairwiseSeed ShareKind = iota
	// ShareSelfMask 自掩码份额，只允许对存活用户披露。
	ShareSelfMask
)

// String 返回份额类型的可读名称。
func (k ShareKind) String() string {
	switch k {
	case SharePairwiseSeed:
		return "pairwise-seed"
	case ShareSelfMask:
		return "self-mask"
	default:
		return fmt.Sprintf("unknown-share-kind(%d)", int(k))
	}
}

// Signature 是某个客户端对存活集合的签名。
type Signature struct {
	// SignerID 签名客户端标识。
	SignerID string
	// Signed 被签名的存活集合成员；守卫会规范化后再交给验证器，
	// 并要求其与服务器提出的 U3 是同一集合。
	Signed []string
	// Value 不透明签名值，由注入的 SignatureVerifier 解释。
	Value []byte
}

// SignatureVerifier 校验 signer 对某条存活集合消息的签名是否有效。
// 守卫不绑定具体密码学原语，由客户端在构造时注入实现
// （例如内部基于 ed25519 或 HMAC）。
type SignatureVerifier func(signer string, message, signature []byte) bool

// CanonicalAliveSetMessage 返回存活集合的规范签名字节串：
// 成员去重并按字典序排序后以换行拼接，保证不同顺序的同一集合产生同一消息。
func CanonicalAliveSetMessage(aliveSet []string) []byte {
	members := append([]string(nil), aliveSet...)
	sort.Strings(members)
	return []byte("alive-set:" + strings.Join(members, "\n"))
}

// DecryptionRequest 是服务器在解密阶段发出的一次份额披露请求。
type DecryptionRequest struct {
	// Dropped 服务器声称掉线、请求其成对种子份额的用户。
	Dropped []string
	// Survived 服务器声称存活、请求其自掩码份额的用户。
	Survived []string
}

// Disclosure 是守卫批准后返回的单条披露授权。
type Disclosure struct {
	// UserID 被授权披露的用户。
	UserID string
	// Kind 授权披露的份额类型。
	Kind ShareKind
}

// 守卫可能返回的哨兵错误，调用方用 errors.Is 判定。
var (
	// ErrSessionTerminated 会话已终止，终止后的守卫不再接受任何状态推进或披露。
	ErrSessionTerminated = errors.New("netsec: session terminated")
	// ErrInvalidPhase 方法调用与当前协议阶段不匹配，或试图跳阶段/回退。
	ErrInvalidPhase = errors.New("netsec: invalid phase for operation")
	// ErrEmptyUserID 输入集合中出现空用户标识。
	ErrEmptyUserID = errors.New("netsec: empty user id")
	// ErrAliveSetNotRecorded 尚未在掩码输入阶段记录存活集合 U2。
	ErrAliveSetNotRecorded = errors.New("netsec: alive set U2 not recorded")
	// ErrU3NotSubsetOfU2 服务器给出的 U3 不是掩码阶段观察到的 U2 的子集。
	ErrU3NotSubsetOfU2 = errors.New("netsec: U3 is not a subset of U2")
	// ErrThresholdNotMet U3 成员数或有效签名数低于阈值 t。
	ErrThresholdNotMet = errors.New("netsec: threshold t not met")
	// ErrSignerNotInU3 签名者不在被签名的存活集合 U3 内。
	ErrSignerNotInU3 = errors.New("netsec: signer is not in U3")
	// ErrBadSignature 签名验证失败，或签名的存活集合与服务器给出的 U3 不一致。
	ErrBadSignature = errors.New("netsec: invalid signature over U3")
	// ErrNilDecryptionRequest 解密请求为空。
	ErrNilDecryptionRequest = errors.New("netsec: nil decryption request")
	// ErrU3NotConfirmed 解密阶段开始前未成功完成一致性签名校验。
	ErrU3NotConfirmed = errors.New("netsec: signed U3 not confirmed")
	// ErrSurvivedNotEqualU3 存活集合 S 与已签名的 U3 不一致。
	ErrSurvivedNotEqualU3 = errors.New("netsec: survived set S is not equal to signed U3")
	// ErrDroppedNotEqualU2MinusU3 掉线集合 D 与 U2\U3 不一致。
	ErrDroppedNotEqualU2MinusU3 = errors.New("netsec: dropped set D is not equal to U2 minus U3")
	// ErrSetsIntersect 掉线集合与存活集合出现同一用户。
	ErrSetsIntersect = errors.New("netsec: dropped set and survived set intersect")
	// ErrShareKindConflict 同一用户被请求披露与其已披露份额互斥的另一种份额。
	ErrShareKindConflict = errors.New("netsec: conflicting share kind for same user")
	// ErrDecryptionAlreadyHandled 一次会话只允许一次解密请求，重复请求必须拒绝。
	ErrDecryptionAlreadyHandled = errors.New("netsec: decryption request already handled")
)

// Guard 是安全聚合客户端的份额披露守卫。
//
// 守卫本身不接触任何密钥材料，只负责协议阶段、存活集合、签名门槛与
// 份额类型互斥的状态机判定，批准后由调用方按 Disclosure 交出真实份额。
// 所有方法对并发使用安全。任何解密阶段的安全校验失败都会 fail-closed
// 地终止会话，终止后永远不再披露任何份额。
type Guard struct {
	mu sync.Mutex

	threshold int
	verifier  SignatureVerifier

	phase      Phase
	terminated bool

	u2 map[string]bool // 掩码输入阶段观察到的存活集合
	u3 map[string]bool // 一致性阶段校验通过的已签名存活集合

	disclosed       map[string]ShareKind // 已披露用户 -> 份额类型
	decryptionFired bool
}

// NewGuard 创建一个阈值为 t、使用 verifier 校验签名的守卫，起始于广播密钥阶段。
// t 必须为正数，verifier 不能为 nil。
func NewGuard(t int, verifier SignatureVerifier) (*Guard, error) {
	if t <= 0 {
		return nil, fmt.Errorf("%w: threshold must be positive, got %d", ErrThresholdNotMet, t)
	}
	if verifier == nil {
		return nil, errors.New("netsec: nil signature verifier")
	}
	return &Guard{
		threshold: t,
		verifier:  verifier,
		phase:     PhaseBroadcastKeys,
		disclosed: make(map[string]ShareKind),
	}, nil
}

// Phase 返回当前协议阶段。
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

// AdvanceTo 将阶段推进到 next。阶段只能严格前进一步，不能跳跃或回退。
func (g *Guard) AdvanceTo(next Phase) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.terminated {
		return ErrSessionTerminated
	}
	if next != g.phase+1 {
		return fmt.Errorf("%w: cannot move from %s to %s", ErrInvalidPhase, g.phase, next)
	}
	g.phase = next
	return nil
}

// RecordAliveSet 在掩码输入阶段记录本客户端观察到的存活集合 U2。
// 同一阶段内可以用最新观察覆盖；空用户标识会被拒绝。
func (g *Guard) RecordAliveSet(aliveUsers []string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.terminated {
		return ErrSessionTerminated
	}
	if g.phase != PhaseMaskedInput {
		return fmt.Errorf("%w: RecordAliveSet requires %s, current %s", ErrInvalidPhase, PhaseMaskedInput, g.phase)
	}
	alive, err := toSet(aliveUsers)
	if err != nil {
		return err
	}
	g.u2 = alive
	return nil
}

// ConfirmAliveSet 在一致性签名阶段校验服务器给出的存活集合 U3 及配套签名。
//
// 校验条件全部满足才会锁定 U3：U3 是 U2 的子集、|U3| >= t、
// 至少 t 个签名对同一个 U3 有效，且每个签名者都属于 U3。
// 校验失败返回错误且不改变状态，调用方可在同一阶段修正后重试。
func (g *Guard) ConfirmAliveSet(u3Users []string, signatures []Signature) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.terminated {
		return ErrSessionTerminated
	}
	if g.phase != PhaseConsistencySign {
		return fmt.Errorf("%w: ConfirmAliveSet requires %s, current %s", ErrInvalidPhase, PhaseConsistencySign, g.phase)
	}
	if g.u3 != nil {
		return fmt.Errorf("%w: U3 already confirmed", ErrInvalidPhase)
	}
	if len(g.u2) == 0 {
		return ErrAliveSetNotRecorded
	}

	u3, err := toSet(u3Users)
	if err != nil {
		return err
	}
	for id := range u3 {
		if !g.u2[id] {
			return fmt.Errorf("%w: user %q not observed in U2", ErrU3NotSubsetOfU2, id)
		}
	}
	if len(u3) < g.threshold {
		return fmt.Errorf("%w: |U3|=%d < t=%d", ErrThresholdNotMet, len(u3), g.threshold)
	}

	message := CanonicalAliveSetMessage(u3Users)
	validSigners := make(map[string]bool)
	for _, sig := range signatures {
		if sig.SignerID == "" {
			return fmt.Errorf("%w: empty signer id", ErrBadSignature)
		}
		if !u3[sig.SignerID] {
			return fmt.Errorf("%w: signer %q", ErrSignerNotInU3, sig.SignerID)
		}
		if validSigners[sig.SignerID] {
			continue
		}
		signedSet, err := toSet(sig.Signed)
		if err != nil {
			return fmt.Errorf("%w: signer %q signed malformed set: %v", ErrBadSignature, sig.SignerID, err)
		}
		if !sameSet(signedSet, u3) {
			return fmt.Errorf("%w: signer %q did not sign the proposed U3", ErrBadSignature, sig.SignerID)
		}
		if !g.verifier(sig.SignerID, message, sig.Value) {
			return fmt.Errorf("%w: signer %q", ErrBadSignature, sig.SignerID)
		}
		validSigners[sig.SignerID] = true
	}
	if len(validSigners) < g.threshold {
		return fmt.Errorf("%w: valid signatures=%d < t=%d", ErrThresholdNotMet, len(validSigners), g.threshold)
	}

	g.u3 = u3
	return nil
}

// HandleDecryptionRequest 在解密阶段处理服务器唯一一次份额披露请求。
//
// 存活集合 S 必须精确等于已签名的 U3，掉线集合 D 必须精确等于 U2\U3，
// D 与 S 不得相交，且同一用户只能披露一种份额：若此前已披露过该用户的
// 一种份额，再请求另一种必须拒绝。任何安全条件失败都会终止会话。
// 成功时返回授权披露清单并锁定本次会话，后续解密请求一律拒绝。
func (g *Guard) HandleDecryptionRequest(req *DecryptionRequest) ([]Disclosure, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.terminated {
		return nil, ErrSessionTerminated
	}
	if g.phase != PhaseDecrypt {
		return nil, fmt.Errorf("%w: HandleDecryptionRequest requires %s, current %s", ErrInvalidPhase, PhaseDecrypt, g.phase)
	}
	if g.decryptionFired {
		g.terminateLocked()
		return nil, ErrDecryptionAlreadyHandled
	}
	if g.u3 == nil {
		g.terminateLocked()
		return nil, ErrU3NotConfirmed
	}
	if req == nil {
		// 空请求属于调用方编程错误，不消耗唯一一次解密机会，也无需终止。
		return nil, ErrNilDecryptionRequest
	}

	dropped, err := toSet(req.Dropped)
	if err != nil {
		g.terminateLocked()
		return nil, err
	}
	survived, err := toSet(req.Survived)
	if err != nil {
		g.terminateLocked()
		return nil, err
	}

	// 同一用户在同一请求里同时被归入 D 与 S，两类份额将被同时交出。
	if intersects(dropped, survived) {
		g.terminateLocked()
		return nil, ErrSetsIntersect
	}

	// 同一用户跨请求索要互斥的另一种份额（先 D 后 S 或先 S 后 D）。
	for id := range dropped {
		if kind, ok := g.disclosed[id]; ok && kind != SharePairwiseSeed {
			g.terminateLocked()
			return nil, fmt.Errorf("%w: user %q already disclosed %s", ErrShareKindConflict, id, kind)
		}
	}
	for id := range survived {
		if kind, ok := g.disclosed[id]; ok && kind != ShareSelfMask {
			g.terminateLocked()
			return nil, fmt.Errorf("%w: user %q already disclosed %s", ErrShareKindConflict, id, kind)
		}
	}

	if !sameSet(survived, g.u3) {
		g.terminateLocked()
		return nil, ErrSurvivedNotEqualU3
	}

	expectedDropped := make(map[string]bool)
	for id := range g.u2 {
		if !g.u3[id] {
			expectedDropped[id] = true
		}
	}
	if !sameSet(dropped, expectedDropped) {
		g.terminateLocked()
		return nil, ErrDroppedNotEqualU2MinusU3
	}

	disclosures := make([]Disclosure, 0, len(dropped)+len(survived))
	for id := range dropped {
		disclosures = append(disclosures, Disclosure{UserID: id, Kind: SharePairwiseSeed})
		g.disclosed[id] = SharePairwiseSeed
	}
	for id := range survived {
		disclosures = append(disclosures, Disclosure{UserID: id, Kind: ShareSelfMask})
		g.disclosed[id] = ShareSelfMask
	}
	sort.Slice(disclosures, func(i, j int) bool {
		if disclosures[i].UserID != disclosures[j].UserID {
			return disclosures[i].UserID < disclosures[j].UserID
		}
		return disclosures[i].Kind < disclosures[j].Kind
	})

	g.decryptionFired = true
	return disclosures, nil
}

// Terminate 显式终止会话。终止后任何阶段推进、一致性确认或份额披露都被拒绝。
// 重复终止是幂等的。
func (g *Guard) Terminate() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.terminateLocked()
}

// U2 返回掩码输入阶段记录的存活集合快照，未记录时返回 nil。
func (g *Guard) U2() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return keysOf(g.u2)
}

// U3 返回一致性阶段确认的已签名存活集合快照，未确认时返回 nil。
func (g *Guard) U3() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return keysOf(g.u3)
}

func (g *Guard) terminateLocked() {
	g.terminated = true
}

// toSet 去重为集合；空用户标识视为非法输入。
func toSet(ids []string) (map[string]bool, error) {
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id == "" {
			return nil, ErrEmptyUserID
		}
		set[id] = true
	}
	return set, nil
}

func sameSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for id := range a {
		if !b[id] {
			return false
		}
	}
	return true
}

func intersects(a, b map[string]bool) bool {
	small, large := a, b
	if len(b) < len(a) {
		small, large = b, a
	}
	for id := range small {
		if large[id] {
			return true
		}
	}
	return false
}

func keysOf(set map[string]bool) []string {
	if set == nil {
		return nil
	}
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
