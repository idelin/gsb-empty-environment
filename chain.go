package netsec

import (
	"fmt"
	"time"
)

// Chain 是一条有序的委托凭证链。
//
// Tokens 按委托顺序排列：Tokens[0] 为根凭证（由资源服务信任的签发者
// 直接签发），其后每个元素都是前一个元素的被委托方再向下签发的凭证，
// 最后一个元素的受众即实际访问资源的主体。
type Chain struct {
	Tokens []*Token
}

// VerifyOptions 控制委托链的校验行为。
type VerifyOptions struct {
	// TrustedIssuers 是资源服务信任的根签发者 DID 集合。
	// 链上第一张凭证的 iss 必须命中该集合，否则拒绝。
	TrustedIssuers map[string]bool

	// Now 为校验所用的当前时间，默认取 time.Now()。测试可注入固定时钟。
	Now time.Time
}

// ValidateChain 校验整条委托链的内部一致性，但不检查具体资源访问。
//
// 规则（任一不满足即返回对应的哨兵错误）：
//   - 链非空，且每张凭证的签名都能被其签发者公钥验证；
//   - 根凭证的签发者位于可信签发者集合内；
//   - 所有凭证在 Now 时刻均未过期；
//   - 相邻两跳首尾相接：子凭证的 iss 等于父凭证的 aud，
//     且子凭证的 prf 精确引用父凭证编码；
//   - 过期时间只减不增：子凭证的 exp 不得晚于父凭证的 exp；
//   - 能力只减不增：子凭证的每条能力都必须被父凭证的某条能力覆盖。
func ValidateChain(chain Chain, opts VerifyOptions) error {
	if len(chain.Tokens) == 0 {
		return ErrEmptyChain
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}

	for i, token := range chain.Tokens {
		if token == nil {
			return fmt.Errorf("%w: token at hop %d is nil", ErrMalformedToken, i)
		}

		// 每张凭证先独立验签（Parse 已验过，这里防御重新手工构造的 Token）。
		parsed, err := Parse(token.Encode())
		if err != nil {
			return fmt.Errorf("hop %d: %w", i, err)
		}

		if !now.Before(parsed.Expires()) {
			return fmt.Errorf("hop %d: %w", i, ErrExpired)
		}

		if i == 0 {
			if !opts.TrustedIssuers[parsed.Issuer()] {
				return fmt.Errorf("%w: %s", ErrUntrustedRoot, parsed.Issuer())
			}
			if parsed.Proof() != "" {
				return fmt.Errorf("hop 0: %w: root token must not carry a proof", ErrMalformedToken)
			}
			continue
		}

		parent := chain.Tokens[i-1]

		// 受众首尾相接：父凭证把能力委托给了谁，谁才有资格签发下一跳。
		if parsed.Issuer() != parent.Audience() {
			return fmt.Errorf("hop %d: %w: issuer %s is not the parent audience %s",
				i, ErrAudienceMismatch, parsed.Issuer(), parent.Audience())
		}
		if parsed.Proof() != parent.Encode() {
			return fmt.Errorf("hop %d: %w: proof does not reference the parent token",
				i, ErrAudienceMismatch)
		}

		// 过期时间不得比前一跳更晚（相等允许）。
		if parsed.Expires().After(parent.Expires()) {
			return fmt.Errorf("hop %d: %w: child expiry %s is after parent expiry %s",
				i, ErrExpiryExtended, parsed.Expires(), parent.Expires())
		}

		// 能力必须是前一跳的子集。
		if !CapabilitiesSubset(parsed.Capabilities(), parent.Capabilities()) {
			return fmt.Errorf("hop %d: %w: child caps %v exceed parent caps %v",
				i, ErrCapabilityEscalated, parsed.Capabilities(), parent.Capabilities())
		}
	}
	return nil
}

// VerifyAccess 是资源服务的入口：先校验委托链，再确认末端主体被授予的
// 能力集合确实覆盖了本次请求的能力 want。
func VerifyAccess(chain Chain, want Capability, opts VerifyOptions) error {
	if err := ValidateChain(chain, opts); err != nil {
		return err
	}
	leaf := chain.Tokens[len(chain.Tokens)-1]
	if !coversAny(leaf.Capabilities(), want) {
		return fmt.Errorf("%w: %+v", ErrCapabilityNotDelegated, want)
	}
	return nil
}
