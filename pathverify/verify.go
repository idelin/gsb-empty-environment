package pathverify

import (
	"crypto/x509"
	"time"
)

// KeyUsage 复用标准库位掩码，保证调用方零成本迁移。
type KeyUsage = x509.KeyUsage

// ExtKeyUsage 复用标准库扩展用途常量（含 AnyExtendedKeyUsage 豁免语义）。
type ExtKeyUsage = x509.ExtKeyUsage

const (
	ExtKeyUsageAny             = x509.ExtKeyUsageAny
	ExtKeyUsageServerAuth      = x509.ExtKeyUsageServerAuth
	ExtKeyUsageClientAuth      = x509.ExtKeyUsageClientAuth
	ExtKeyUsageCodeSigning     = x509.ExtKeyUsageCodeSigning
	ExtKeyUsageEmailProtection = x509.ExtKeyUsageEmailProtection
)

// Options 控制一次严格验证的输入与策略开关。
type Options struct {
	// Leaf 待验证的终端实体证书（叶证书）。必填。
	Leaf *x509.Certificate
	// Roots 受信任根集合（Trust Anchors）。必填。
	Roots *CertPool
	// Intermediates 非信任中间证书池，可为空。
	Intermediates *CertPool
	// KeyUsages 叶证书的目标 KeyUsage 要求；为空时要求 digitalSignature。
	KeyUsages []KeyUsage
	// EKU 目标扩展用途（如 ExtKeyUsageServerAuth）。为 nil 时不做 EKU 约束。
	EKU []ExtKeyUsage
	// DNSName 需要与叶证书匹配的主机名；为空则跳过主机名匹配。
	DNSName string
	// CRLs 已解析的 CRL 集合，用于吊销终态判定；为空表示不强制吊销检查。
	CRLs []*x509.RevocationList
	// CurrentTime 验证时刻，为零值时取系统当前时间。
	CurrentTime timeValue
	// IgnoreAnchorValidity 为 true 时跳过信任锚有效期校验。默认（false）严格校验，
	// 用于拦截“旧根已过期”的跨签主路径；RFC 5280 本身不强制锚点有效期，
	// 需要纯规范行为时可显式置 true。
	IgnoreAnchorValidity bool
	// DisableLeafKeyUsageCheck 为 true 时跳过叶证书 KeyUsage 目标用途检查。
	DisableLeafKeyUsageCheck bool
}

// Result 为一次成功验证的输出。
type Result struct {
	// Chain 验证通过的链，根在前、叶子在后。
	Chain []*x509.Certificate
	// Summaries 与 Chain 对齐的诊断摘要。
	Summaries []*CertSummary
}

// Verify 执行多路径构建 + 逐候选链严格验证，返回第一条通过全部检查的链。
// 当某条候选链因有效期、吊销、pathLen 或名称约束失败时，构建器会继续
// 回溯其他可达信任锚的候选链；全部失败时返回带完整 Attempts 诊断的 *Error。
func Verify(opts Options) (*Result, error) {
	if opts.Leaf == nil {
		return nil, fail(ReasonInvalidInput, "leaf certificate is required")
	}
	if opts.Roots == nil || opts.Roots.Len() == 0 {
		return nil, fail(ReasonInvalidInput, "at least one trust anchor is required")
	}
	if opts.Intermediates == nil {
		opts.Intermediates = NewCertPool()
	}
	now := opts.CurrentTime.orNow()

	candidates := buildChains(opts.Leaf, opts.Roots, opts.Intermediates, defaultMaxPathLen)
	if len(candidates) == 0 {
		return nil, fail(ReasonNoValidPath,
			"no structurally signed path from leaf to any trust anchor")
	}

	topErr := fail(ReasonNoValidPath,
		"all %d candidate chain(s) failed strict verification", len(candidates))
	for _, chain := range candidates {
		if err := verifyChain(chain, opts, now); err != nil {
			topErr.Attempts = append(topErr.Attempts, Attempt{
				Chain: summarizeChain(chain),
				Err:   err,
			})
			continue
		}
		return &Result{Chain: chain, Summaries: summarizeChain(chain)}, nil
	}
	return nil, topErr
}

// verifyChain 对根在前、叶子在后的单条链执行 RFC 5280 策略检查。
// 检查顺序与 RFC 5280 6.1 基本对齐，任一环节失败立即拒绝该路径。
func verifyChain(chain []*x509.Certificate, opts Options, now time.Time) *Error {
	leaf := chain[len(chain)-1]

	// (a) 关键扩展拦截：任何无法识别且 Critical=true 的扩展直接拒绝。
	for _, c := range chain {
		if len(c.UnhandledCriticalExtensions) > 0 {
			return fail(ReasonCriticalExtension,
				"certificate %q has unhandled critical extension(s): %v",
				c.Subject.String(), c.UnhandledCriticalExtensions)
		}
	}

	// (b) 逐张签名回溯 + 有效期（锚点有效期按开关处理）。
	for i, c := range chain {
		if i == 0 {
			if !opts.IgnoreAnchorValidity && !inValidityWindow(c, now) {
				return fail(ReasonExpired, "trust anchor %q is not valid at %s",
					c.Subject.String(), now.Format(time.RFC3339))
			}
			continue
		}
		parent := chain[i-1]
		if err := checkSignedBy(c, parent); err != nil {
			return fail(ReasonSignature, "certificate %q is not signed by %q: %v",
				c.Subject.String(), parent.Subject.String(), err)
		}
		if !inValidityWindow(c, now) {
			return fail(ReasonExpired,
				"certificate %q is not valid at %s (NotBefore=%s NotAfter=%s)",
				c.Subject.String(), now.Format(time.RFC3339),
				c.NotBefore.UTC().Format(time.RFC3339), c.NotAfter.UTC().Format(time.RFC3339))
		}
	}

	// (c) BasicConstraints / CA 位 / KeyUsage：所有承担签发职责的证书必须是合规 CA。
	for i := 0; i < len(chain)-1; i++ {
		ca := chain[i]
		if !ca.BasicConstraintsValid || !ca.IsCA {
			return fail(ReasonNotCA,
				"certificate %q issues another certificate but CA=true is absent",
				ca.Subject.String())
		}
		// RFC 5280 4.2.1.3：合规 CA 若携带 KeyUsage，必须置 keyCertSign。
		if ca.KeyUsage != 0 && ca.KeyUsage&x509.KeyUsageCertSign == 0 {
			return fail(ReasonKeyUsage,
				"intermediate CA %q KeyUsage lacks keyCertSign", ca.Subject.String())
		}
	}
	if leaf.BasicConstraintsValid && leaf.IsCA {
		return fail(ReasonNotCA, "leaf certificate %q must not assert CA=true", leaf.Subject.String())
	}

	// (d) 叶证书 KeyUsage 目标用途。
	requiredKU := x509.KeyUsageDigitalSignature
	if len(opts.KeyUsages) > 0 {
		requiredKU = 0
		for _, u := range opts.KeyUsages {
			requiredKU |= u
		}
	}
	if !opts.DisableLeafKeyUsageCheck && leaf.KeyUsage != 0 && leaf.KeyUsage&requiredKU == 0 {
		return fail(ReasonKeyUsage,
			"leaf %q KeyUsage %v does not include required %v",
			leaf.Subject.String(), leaf.KeyUsage, requiredKU)
	}

	// (e) EKU 链式收紧：目标用途必须被叶证书与每个携带非空 EKU 的 CA 同时放行，
	// AnyExtendedKeyUsage 作为豁免。
	if len(opts.EKU) > 0 {
		if err := verifyEKU(chain, opts.EKU); err != nil {
			return err
		}
	}

	// (f) RFC 5280 6.1.4 pathLenConstraint 精确计数。
	if err := verifyPathLen(chain); err != nil {
		return err
	}

	// (g) NameConstraints 累积（permitted 逐层交集 / excluded 逐层并集）。
	if err := verifyNameConstraints(chain); err != nil {
		return err
	}

	// (h) 主机名：存在任何 DNS SAN 时只校验 SAN，彻底忽略 Subject CN。
	if opts.DNSName != "" {
		if err := verifyHostname(leaf, opts.DNSName); err != nil {
			return err
		}
	}

	// (i) CRL 吊销终态判定。
	if len(opts.CRLs) > 0 {
		if err := verifyRevocation(chain, opts.CRLs, now); err != nil {
			return err
		}
	}
	return nil
}

// verifyPathLen 自顶向下处理每一层 CA 的 pathLenConstraint。
// 规则（RFC 5280 6.1.4 (l)）：
//   - 仅非自颁发（Subject != Issuer）中间 CA 消耗一级深度；
//   - 自颁发证书（密钥轮换过渡，Subject == Issuer）不消耗；
//   - 叶证书绝不消耗 pathLenConstraint。
func verifyPathLen(chain []*x509.Certificate) *Error {
	// maxIntermediates 为自当前层向下允许出现的“非自颁发中间 CA”数量上限。
	// 叶证书不计数，因此初始无约束时使用链长度作为足够大的上界。
	maxIntermediates := len(chain)
	for i, ca := range chain[:len(chain)-1] {
		if ca.MaxPathLen >= 0 && ca.MaxPathLen < maxIntermediates {
			// MaxPathLen==0 的 CA 可合法签发叶证书，但不能再签任何下级非自颁发中间 CA。
			maxIntermediates = ca.MaxPathLen
		}
		nonSelfIssued := 0
		for j := i + 1; j < len(chain)-1; j++ {
			if !isSelfIssued(chain[j]) {
				nonSelfIssued++
			}
		}
		if nonSelfIssued > maxIntermediates {
			return fail(ReasonPathLen,
				"CA %q pathLenConstraint=%d violated by %d non-self-issued intermediate CA(s)",
				ca.Subject.String(), ca.MaxPathLen, nonSelfIssued)
		}
	}
	return nil
}

func isSelfIssued(c *x509.Certificate) bool {
	return c.Subject.String() == c.Issuer.String()
}

// verifyEKU 校验目标用途在整条链上的交集非空。
func verifyEKU(chain []*x509.Certificate, wanted []ExtKeyUsage) *Error {
	allows := func(c *x509.Certificate) bool {
		if len(c.ExtKeyUsage) == 0 {
			return true // 未携带 EKU 扩展：不参与收紧。
		}
		for _, have := range c.ExtKeyUsage {
			if have == x509.ExtKeyUsageAny {
				return true
			}
			for _, w := range wanted {
				if have == w {
					return true
				}
			}
		}
		return false
	}
	for _, c := range chain {
		if !allows(c) {
			return fail(ReasonEKU,
				"certificate %q EKU %v does not permit required usage %v",
				c.Subject.String(), c.ExtKeyUsage, wanted)
		}
	}
	return nil
}
