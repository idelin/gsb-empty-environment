// Package orchard 实现区块验证阶段调用的 Orchard 束准入检查。
//
// 本包只负责规范编码与双花相关的结构性准入规则：
//
//  1. flags 仅最低两位有定义，保留位非零即拒绝；
//  2. 每个动作的废止符（nullifier）与注记承诺（cmx）是 32 字节小端
//     Pallas 基域元素编码，取值必须严格小于基域模数；
//  3. 同一束内废止符不得重复；
//  4. 废止符不得出现在调用方提供的链上已花费集合中；
//  5. 锚点必须是调用方提供的已知树根集合中的成员；
//  6. 同一区块内多个束的废止符同样不得重复。
//
// 证明验证与签名验证不属于本包职责，由调用方通过注入的
// ProofVerifier / SignatureVerifier 回调完成；回调为 nil 时跳过对应步骤，
// 便于调用方在不同阶段自行决定何时执行昂贵的密码学校验。
package orchard

import (
	"errors"
	"fmt"
)

// PallasBaseFieldModulus 是 Pallas 基域模数
// 0x40000000000000000000000000000000224698fc094cf91b992d30ed00000001
// 的 32 字节小端编码。
var PallasBaseFieldModulus = [32]byte{
	0x01, 0x00, 0x00, 0x00, 0xed, 0x30, 0x2d, 0x99,
	0x1b, 0xf9, 0x4c, 0x09, 0xfc, 0x98, 0x46, 0x22,
	0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x40,
}

// definedFlagsMask 标志位掩码：只有最低两位有定义。
const definedFlagsMask byte = 0x03

// 结构性准入错误。调用方可用 errors.Is 判定具体拒绝原因。
var (
	// ErrReservedFlags 表示 flags 的保留位（最低两位之外）非零。
	ErrReservedFlags = errors.New("orchard: flags contain reserved (undefined) bits")
	// ErrNonCanonicalFieldElement 表示域元素编码不规范：取值不小于模数。
	ErrNonCanonicalFieldElement = errors.New("orchard: non-canonical field element encoding (value >= Pallas base field modulus)")
	// ErrDuplicateNullifier 表示束内或区块内出现重复废止符。
	ErrDuplicateNullifier = errors.New("orchard: duplicate nullifier")
	// ErrSpentNullifier 表示废止符已存在于链上已花费集合。
	ErrSpentNullifier = errors.New("orchard: nullifier already spent on chain")
	// ErrUnknownAnchor 表示锚点不属于已知树根集合。
	ErrUnknownAnchor = errors.New("orchard: anchor is not a known note commitment tree root")
	// ErrEmptyBundle 表示束中没有任何动作，无法构成有效束。
	ErrEmptyBundle = errors.New("orchard: bundle contains no actions")
)

// KnownRoots 提供调用方已知的（历史）笔记承诺树树根集合。
// 锚点成员资格判定由调用方实现，例如直接持有 map 或查询链状态。
type KnownRoots interface {
	ContainsRoot(anchor [32]byte) bool
}

// SpentNullifiers 提供链上已花费废止符集合。
type SpentNullifiers interface {
	ContainsNullifier(nullifier [32]byte) bool
}

// RootSet 是基于 map 的 KnownRoots 适配器。
type RootSet map[[32]byte]struct{}

// ContainsRoot 实现 KnownRoots。
func (s RootSet) ContainsRoot(anchor [32]byte) bool {
	_, ok := s[anchor]
	return ok
}

// NullifierSet 是基于 map 的 SpentNullifiers 适配器。
type NullifierSet map[[32]byte]struct{}

// ContainsNullifier 实现 SpentNullifiers。
func (s NullifierSet) ContainsNullifier(nullifier [32]byte) bool {
	_, ok := s[nullifier]
	return ok
}

// Action 是 Orchard 动作中与准入检查相关的字段。
// Nullifier 与 Commitment（cmx）均为 32 字节小端域元素编码。
type Action struct {
	// Nullifier 是本动作花费输入的废止符（nf）。
	Nullifier [32]byte
	// Commitment 是注记承诺（cmx）。
	Commitment [32]byte
}

// Bundle 是一个 Orchard 动作束。
type Bundle struct {
	// Actions 是束内动作列表，顺序即序列化顺序。
	Actions []Action
	// Anchor 是本束所有动作共享的笔记承诺树锚点。
	Anchor [32]byte
	// Flags 是束标志字节，只有最低两位有定义。
	Flags byte
}

// ProofVerifier 由调用方注入，用于验证动作/束的零知识证明。
// 返回 nil 表示证明有效。
type ProofVerifier func(bundle *Bundle) error

// SignatureVerifier 由调用方注入，用于验证束绑定授权签名。
// 返回 nil 表示签名有效。
type SignatureVerifier func(bundle *Bundle) error

// BlockValidator 在单个区块的验证过程中累积已见过的废止符，
// 从而同时保证束内与跨束（区块内）废止符不重复。
//
// BlockValidator 非并发安全；区块内各束应按顺序准入。
type BlockValidator struct {
	roots       KnownRoots
	spent       SpentNullifiers
	proof       ProofVerifier
	signature   SignatureVerifier
	seenInBlock map[[32]byte]struct{}
}

// NewBlockValidator 构造区块级验证器。
//
// roots 为必填项，提供合法锚点集合；spent 可为 nil，表示不查询链上
// 已花费集合（例如调用方另行处理）。proof / signature 为 nil 时
// 跳过对应的注入式验证步骤。
func NewBlockValidator(roots KnownRoots, spent SpentNullifiers, proof ProofVerifier, signature SignatureVerifier) (*BlockValidator, error) {
	if roots == nil {
		return nil, errors.New("orchard: known root set is required for anchor validation")
	}
	return &BlockValidator{
		roots:       roots,
		spent:       spent,
		proof:       proof,
		signature:   signature,
		seenInBlock: make(map[[32]byte]struct{}),
	}, nil
}

// AdmitBundle 对单个束执行准入检查。
//
// 检查全部通过后，束内废止符会被登记到区块级去重集合，供后续束使用。
// 任何结构性错误都会在调用注入的证明/签名验证之前返回，避免对
// 注定被拒绝的束执行昂贵的密码学运算。
func (v *BlockValidator) AdmitBundle(bundle *Bundle) error {
	if bundle == nil {
		return errors.New("orchard: nil bundle")
	}
	if len(bundle.Actions) == 0 {
		return ErrEmptyBundle
	}

	// 1. flags 保留位必须为零。
	if bundle.Flags&^definedFlagsMask != 0 {
		return fmt.Errorf("%w: flags=0x%02x", ErrReservedFlags, bundle.Flags)
	}

	// 2. 锚点本身必须是规范域元素，且属于已知树根集合。
	if !isCanonicalFieldElement(bundle.Anchor) {
		return fmt.Errorf("%w: anchor", ErrNonCanonicalFieldElement)
	}
	if !v.roots.ContainsRoot(bundle.Anchor) {
		return fmt.Errorf("%w: anchor=%x", ErrUnknownAnchor, bundle.Anchor)
	}

	// 3. 逐动作检查规范编码、束内重复、链上已花费与区块内重复。
	local := make(map[[32]byte]struct{}, len(bundle.Actions))
	for i := range bundle.Actions {
		action := &bundle.Actions[i]

		if !isCanonicalFieldElement(action.Nullifier) {
			return fmt.Errorf("%w: action[%d].nullifier", ErrNonCanonicalFieldElement, i)
		}
		if !isCanonicalFieldElement(action.Commitment) {
			return fmt.Errorf("%w: action[%d].cmx", ErrNonCanonicalFieldElement, i)
		}

		if _, dup := local[action.Nullifier]; dup {
			return fmt.Errorf("%w: action[%d].nullifier=%x repeated within bundle", ErrDuplicateNullifier, i, action.Nullifier)
		}
		local[action.Nullifier] = struct{}{}

		if v.spent != nil && v.spent.ContainsNullifier(action.Nullifier) {
			return fmt.Errorf("%w: nullifier=%x", ErrSpentNullifier, action.Nullifier)
		}

		if _, dup := v.seenInBlock[action.Nullifier]; dup {
			return fmt.Errorf("%w: nullifier=%x repeated across bundles in block", ErrDuplicateNullifier, action.Nullifier)
		}
	}

	// 4. 结构性检查通过后，再执行调用方注入的密码学验证。
	if v.proof != nil {
		if err := v.proof(bundle); err != nil {
			return fmt.Errorf("orchard: proof verification failed: %w", err)
		}
	}
	if v.signature != nil {
		if err := v.signature(bundle); err != nil {
			return fmt.Errorf("orchard: signature verification failed: %w", err)
		}
	}

	// 5. 全部通过，登记区块级废止符。
	for nf := range local {
		v.seenInBlock[nf] = struct{}{}
	}
	return nil
}

// ValidateBundle 对单个束做独立准入检查，不保留任何跨束状态。
//
// 适用于 mempool 单束校验等没有区块上下文的场景；需要保证多个束之间
// 废止符不重复时，应使用 BlockValidator.AdmitBundle。
func ValidateBundle(bundle *Bundle, roots KnownRoots, spent SpentNullifiers, proof ProofVerifier, signature SignatureVerifier) error {
	validator, err := NewBlockValidator(roots, spent, proof, signature)
	if err != nil {
		return err
	}
	return validator.AdmitBundle(bundle)
}

// isCanonicalFieldElement 判断 32 字节小端编码是否为规范 Pallas 基域元素，
// 即其表示的整数严格小于基域模数。
//
// 小端序下最高有效字节位于下标 31，因此从后往前按字节比较即可判定大小，
// 无需引入大整数运算。
func isCanonicalFieldElement(encoded [32]byte) bool {
	for i := len(encoded) - 1; i >= 0; i-- {
		switch {
		case encoded[i] < PallasBaseFieldModulus[i]:
			return true
		case encoded[i] > PallasBaseFieldModulus[i]:
			return false
		}
	}
	// 与模数完全相等，不属于合法域元素。
	return false
}
