package samlsec

import (
	"crypto"
	"time"
)

// ReplayCache 是单次断言 ID 防重放抽象。生产环境可替换为 Redis 等共享存储，
// 测试与单进程部署可使用 NewMemoryReplayCache。
type ReplayCache interface {
	// SeenBefore 原子性地登记 id：首次出现返回 false，重复出现返回 true。
	SeenBefore(id string) (bool, error)
}

// Options 汇总 Validate 的全部安全策略。
type Options struct {
	// TrustedKey 为 IdP 可信公钥。KeyInfo 中的证书/公钥永远不被信任，
	// 攻击者无法通过在 XML 内自带密钥完成验签。
	TrustedKey crypto.PublicKey

	// ExpectedAudience 为 SP 在 AudienceRestriction 中要求出现的受众。
	ExpectedAudience string

	// ExpectedRecipient 为 Bearer SubjectConfirmationData 的 Recipient。
	ExpectedRecipient string

	// ExpectedInResponseTo 为期望的 AuthnRequest ID；空串表示不校验。
	ExpectedInResponseTo string

	// ExpectedIssuer 可选，非空时要求 Response/Assertion Issuer 与之相等。
	ExpectedIssuer string

	// ClockSkew 时间窗容差，默认 60 秒。
	ClockSkew time.Duration

	// Now 注入当前时间，默认 time.Now。测试可固定时钟。
	Now func() time.Time

	// Replay 防重放缓存；nil 表示不执行防重放（不推荐）。
	Replay ReplayCache

	// ParseOptions 透传给严格解析器。
	ParseOptions *ParseOptions
}

// Attribute 是从已验签 Assertion 提取的单个 AttributeStatement 条目。
type Attribute struct {
	Name       string
	NameFormat string
	Values     []string
}

// VerifiedAssertion 是验证成功后唯一允许业务层读取的数据视图。
// 所有字段均来自 Reference 绑定并通过摘要/签名校验的同一节点子树。
type VerifiedAssertion struct {
	AssertionID string
	Issuer      string
	Subject     string
	Audiences   []string
	Attributes  []Attribute

	// ResponseID 为根 Response 的 ID，便于上层关联日志。
	ResponseID string
	// ResponseIssuer 为根 Response 的 Issuer（可能为空字符串）。
	ResponseIssuer string

	NotBefore    time.Time
	NotOnOrAfter time.Time

	// raw 保留原始字节，供审计，不向业务暴露可绕过验证的 DOM。
	raw []byte
	doc *Document
}

// Validate 是包的主入口：解析 -> 结构硬校验 -> 验签 -> SAML 语义校验 ->
// 防重放 -> 从可信节点提取业务身份。任何一步失败都返回带哨兵原因的错误，
// 成功时返回的 VerifiedAssertion 与验签节点严格同源。
func Validate(raw []byte, opts Options) (*VerifiedAssertion, error) {
	doc, err := Parse(raw, opts.ParseOptions)
	if err != nil {
		return nil, err
	}
	res, err := verifySignature(doc, opts.TrustedKey)
	if err != nil {
		return nil, err
	}

	assertion, err := locateAssertion(doc, res)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	if opts.Now != nil {
		now = opts.Now().UTC()
	}
	skew := opts.ClockSkew
	if skew == 0 {
		skew = 60 * time.Second
	}

	if err := validateIssuers(doc, assertion, opts.ExpectedIssuer); err != nil {
		return nil, err
	}
	notBefore, notOnOrAfter, err := validateConditions(assertion, opts.ExpectedAudience, now, skew)
	if err != nil {
		return nil, err
	}
	if err := validateBearer(assertion, opts, now, skew); err != nil {
		return nil, err
	}

	assertionID, ok := assertion.ID()
	if !ok || assertionID == "" {
		return nil, wrapErr("validate", ErrRequiredField, "Assertion ID missing")
	}
	if opts.Replay != nil {
		seen, err := opts.Replay.SeenBefore(assertionID)
		if err != nil {
			return nil, wrapErr("validate", ErrReplay, "replay cache error: %v", err)
		}
		if seen {
			return nil, wrapErr("validate", ErrReplay, "assertion ID %s already consumed", assertionID)
		}
	}

	out := buildVerified(doc, assertion, raw, notBefore, notOnOrAfter)
	return out, nil
}

// locateAssertion 强制业务读取目标与验签目标同源：
//   - 签名覆盖 Assertion：直接返回该已验签 Assertion；
//   - 签名覆盖 Response：Assertion 必须是根的唯一直接子元素。
func locateAssertion(doc *Document, res *signatureResult) (*Node, error) {
	if res.signed.Is(NSSAML, "Assertion") {
		return res.signed, nil
	}
	var direct []*Node
	for _, c := range doc.Root.Children {
		if c.Is(NSSAML, "Assertion") {
			direct = append(direct, c)
		}
	}
	if len(direct) != 1 {
		return nil, wrapErr("validate", ErrAssertionNotFound,
			"response-signed message requires exactly one direct Assertion, got %d", len(direct))
	}
	return direct[0], nil
}

func validateIssuers(doc *Document, assertion *Node, expected string) error {
	assertionIssuerNode := assertion.FindChild(NSSAML, "Issuer")
	if assertionIssuerNode == nil {
		return wrapErr("validate", ErrRequiredField, "Assertion/Issuer missing")
	}
	assertionIssuer := assertionIssuerNode.TextContent()
	if assertionIssuer == "" {
		return wrapErr("validate", ErrRequiredField, "Assertion/Issuer is empty")
	}

	responseIssuerNode := doc.Root.FindChild(NSSAML, "Issuer")
	if responseIssuerNode != nil {
		responseIssuer := responseIssuerNode.TextContent()
		if responseIssuer != assertionIssuer {
			return wrapErr("validate", ErrCondition,
				"Response Issuer %q != Assertion Issuer %q", responseIssuer, assertionIssuer)
		}
	}
	if expected != "" && assertionIssuer != expected {
		return wrapErr("validate", ErrCondition,
			"issuer %q does not match expected %q", assertionIssuer, expected)
	}
	return nil
}

func validateConditions(assertion *Node, audience string, now time.Time, skew time.Duration) (time.Time, time.Time, error) {
	conditions := assertion.FindChild(NSSAML, "Conditions")
	if conditions == nil {
		return time.Time{}, time.Time{}, wrapErr("validate", ErrRequiredField, "Conditions missing")
	}

	notBefore := time.Time{}
	notOnOrAfter := time.Time{}
	if v, ok := conditions.AttrValue("", "NotBefore"); ok && v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return notBefore, notOnOrAfter, wrapErr("validate", ErrCondition, "bad NotBefore: %v", err)
		}
		notBefore = t
	}
	if v, ok := conditions.AttrValue("", "NotOnOrAfter"); ok && v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return notBefore, notOnOrAfter, wrapErr("validate", ErrCondition, "bad NotOnOrAfter: %v", err)
		}
		notOnOrAfter = t
	}

	if !notBefore.IsZero() && now.Add(skew).Before(notBefore) {
		return notBefore, notOnOrAfter, wrapErr("validate", ErrCondition,
			"assertion NotBefore %s is in the future", notBefore.Format(time.RFC3339))
	}
	if !notOnOrAfter.IsZero() && !now.Add(-skew).Before(notOnOrAfter) {
		return notBefore, notOnOrAfter, wrapErr("validate", ErrCondition,
			"assertion NotOnOrAfter %s has passed", notOnOrAfter.Format(time.RFC3339))
	}

	restrictions := conditions.ChildElements(NSSAML, "AudienceRestriction")
	if len(restrictions) == 0 {
		return notBefore, notOnOrAfter, wrapErr("validate", ErrCondition,
			"no AudienceRestriction present")
	}
	for _, r := range restrictions {
		audiences := r.ChildElements(NSSAML, "Audience")
		if len(audiences) == 0 {
			return notBefore, notOnOrAfter, wrapErr("validate", ErrCondition,
				"empty AudienceRestriction")
		}
		matched := false
		for _, a := range audiences {
			if a.TextContent() == audience {
				matched = true
				break
			}
		}
		if !matched {
			return notBefore, notOnOrAfter, wrapErr("validate", ErrCondition,
				"audience %q not satisfied", audience)
		}
	}
	return notBefore, notOnOrAfter, nil
}

func validateBearer(assertion *Node, opts Options, now time.Time, skew time.Duration) error {
	subject := assertion.FindChild(NSSAML, "Subject")
	if subject == nil {
		return wrapErr("validate", ErrRequiredField, "Subject missing")
	}
	nameID := subject.FindChild(NSSAML, "NameID")
	if nameID == nil || nameID.TextContent() == "" {
		return wrapErr("validate", ErrRequiredField, "Subject/NameID missing or empty")
	}

	confirmation := subject.FindChild(NSSAML, "SubjectConfirmation")
	if confirmation == nil {
		return wrapErr("validate", ErrSubjectConfirmation, "SubjectConfirmation missing")
	}
	if method, _ := confirmation.AttrValue("", "Method"); method != "urn:oasis:names:tc:SAML:2.0:cm:bearer" {
		return wrapErr("validate", ErrSubjectConfirmation,
			"only bearer confirmation is supported, got %q", method)
	}
	data := confirmation.FindChild(NSSAML, "SubjectConfirmationData")
	if data == nil {
		return wrapErr("validate", ErrSubjectConfirmation, "SubjectConfirmationData missing")
	}

	recipient, _ := data.AttrValue("", "Recipient")
	if opts.ExpectedRecipient != "" && recipient != opts.ExpectedRecipient {
		return wrapErr("validate", ErrSubjectConfirmation,
			"Recipient %q != expected %q", recipient, opts.ExpectedRecipient)
	}
	if opts.ExpectedInResponseTo != "" {
		got, _ := data.AttrValue("", "InResponseTo")
		if got != opts.ExpectedInResponseTo {
			return wrapErr("validate", ErrSubjectConfirmation,
				"InResponseTo %q != expected %q", got, opts.ExpectedInResponseTo)
		}
	}

	if v, ok := data.AttrValue("", "NotOnOrAfter"); ok && v != "" {
		deadline, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return wrapErr("validate", ErrSubjectConfirmation, "bad SubjectConfirmationData NotOnOrAfter: %v", err)
		}
		if !now.Add(-skew).Before(deadline) {
			return wrapErr("validate", ErrSubjectConfirmation,
				"bearer SubjectConfirmationData expired at %s", deadline.Format(time.RFC3339))
		}
	}
	return nil
}

func buildVerified(doc *Document, assertion *Node, raw []byte, notBefore, notOnOrAfter time.Time) *VerifiedAssertion {
	out := &VerifiedAssertion{
		raw:          raw,
		doc:          doc,
		AssertionID:  mustID(assertion),
		Issuer:       assertion.FindChild(NSSAML, "Issuer").TextContent(),
		Subject:      assertion.FindDescendant(NSSAML, "NameID").TextContent(),
		NotBefore:    notBefore,
		NotOnOrAfter: notOnOrAfter,
	}
	if rid, ok := doc.Root.ID(); ok {
		out.ResponseID = rid
	}
	if ri := doc.Root.FindChild(NSSAML, "Issuer"); ri != nil {
		out.ResponseIssuer = ri.TextContent()
	}
	for _, r := range assertion.FindAllDescendant(NSSAML, "AudienceRestriction") {
		for _, a := range r.ChildElements(NSSAML, "Audience") {
			out.Audiences = append(out.Audiences, a.TextContent())
		}
	}
	for _, stmt := range assertion.ChildElements(NSSAML, "AttributeStatement") {
		for _, attr := range stmt.ChildElements(NSSAML, "Attribute") {
			name, _ := attr.AttrValue("", "Name")
			entry := Attribute{Name: name}
			entry.NameFormat, _ = attr.AttrValue("", "NameFormat")
			for _, v := range attr.ChildElements(NSSAML, "AttributeValue") {
				entry.Values = append(entry.Values, v.TextContent())
			}
			out.Attributes = append(out.Attributes, entry)
		}
	}
	return out
}

func mustID(n *Node) string {
	v, _ := n.ID()
	return v
}
