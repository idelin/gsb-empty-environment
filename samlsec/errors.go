package samlsec

import (
	"errors"
	"fmt"
)

// 哨兵错误：调用方可以通过 errors.Is 精确区分拒绝原因。所有错误都不可与
// 普通业务“未匹配”混淆——任何一条触发都意味着该 Response 必须被拒绝。
var (
	// ErrMalformed 原始 XML 不是结构良好的文档。
	ErrMalformed = errors.New("samlsec: malformed XML document")
	// ErrDTD 检测到 DTD / DOCTYPE / ENTITY 声明。
	ErrDTD = errors.New("samlsec: DTD/DOCTYPE/ENTITY declarations are forbidden")
	// ErrDuplicateID 文档内出现重复的 ID / xml:id 属性值。
	ErrDuplicateID = errors.New("samlsec: duplicate ID attribute value")
	// ErrDuplicateAttribute 同一个元素标签上出现重复属性名。
	ErrDuplicateAttribute = errors.New("samlsec: duplicate attribute on element")
	// ErrMultipleAssertions 文档包含多个 Assertion（含命名空间混淆变种）。
	ErrMultipleAssertions = errors.New("samlsec: multiple Assertion elements are forbidden")
	// ErrMultipleSignatures 文档包含多个 Signature 元素。
	ErrMultipleSignatures = errors.New("samlsec: multiple Signature elements are forbidden")
	// ErrSensitiveMarkup 安全敏感元素内部出现注释或处理指令。
	ErrSensitiveMarkup = errors.New("samlsec: comment or processing instruction inside security-sensitive element")
	// ErrRootInvalid 根元素不是 SAML 2.0 Response。
	ErrRootInvalid = errors.New("samlsec: root element must be samlp:Response")
	// ErrNamespace 安全关键元素出现在非预期命名空间（标签名混淆攻击）。
	ErrNamespace = errors.New("samlsec: security-critical element in wrong namespace")
	// ErrSignatureNotFound 文档缺少 ds:Signature。
	ErrSignatureNotFound = errors.New("samlsec: missing ds:Signature")
	// ErrSignaturePlacement 签名位置非法（例如被移入 Extensions）。
	ErrSignaturePlacement = errors.New("samlsec: illegal Signature placement")
	// ErrReferenceBinding Reference URI 无法精确绑定到允许路径上的唯一元素。
	ErrReferenceBinding = errors.New("samlsec: Reference URI does not bind to the unique allowed element")
	// ErrUnsupportedAlgorithm 出现白名单之外的规范化、摘要或签名算法。
	ErrUnsupportedAlgorithm = errors.New("samlsec: unsupported cryptographic algorithm")
	// ErrDigestMismatch 重算的 SHA-256 摘要与 DigestValue 不一致。
	ErrDigestMismatch = errors.New("samlsec: reference digest verification failed")
	// ErrSignatureValueMismatch SignatureValue 非对称验签失败。
	ErrSignatureValueMismatch = errors.New("samlsec: SignatureValue verification failed")
	// ErrAssertionNotFound 已验签节点下找不到唯一 Assertion。
	ErrAssertionNotFound = errors.New("samlsec: signed node does not contain exactly one Assertion")
	// ErrCondition SAML Conditions 时间窗或 AudienceRestriction 不满足。
	ErrCondition = errors.New("samlsec: assertion condition failed")
	// ErrSubjectConfirmation Bearer SubjectConfirmation 的 Recipient/InResponseTo 不满足。
	ErrSubjectConfirmation = errors.New("samlsec: bearer SubjectConfirmation failed")
	// ErrReplay 断言 ID 已被消费过（重放）。
	ErrReplay = errors.New("samlsec: assertion replay detected")
	// ErrRequiredField 缺少必填 SAML 字段。
	ErrRequiredField = errors.New("samlsec: required SAML field missing or empty")
)

// ValidationError 把哨兵错误与可读上下文绑定，errors.Is 可穿透到哨兵。
type ValidationError struct {
	Op  string
	Err error
	Msg string
}

func (e *ValidationError) Error() string {
	if e.Msg == "" {
		return e.Op + ": " + e.Err.Error()
	}
	return e.Op + ": " + e.Msg
}

func (e *ValidationError) Unwrap() error { return e.Err }

func wrapErr(op string, sentinel error, format string, args ...any) error {
	return &ValidationError{
		Op:  op,
		Err: sentinel,
		Msg: fmt.Sprintf(format, args...),
	}
}
