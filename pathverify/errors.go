package pathverify

import (
	"fmt"
	"strings"
)

// Reason 以机器可读的方式描述一条候选链（或整体验证）失败的原因。
type Reason string

const (
	// ReasonNoValidPath 图中不存在任何能到达信任锚的结构合法路径，
	// 或所有候选路径均未通过严格验证。
	ReasonNoValidPath Reason = "no_valid_path"
	// ReasonInvalidInput 入参非法（叶子、根池为空等）。
	ReasonInvalidInput Reason = "invalid_input"
	// ReasonSignature 签名回溯失败：子证书无法由父证书公钥验签。
	ReasonSignature Reason = "bad_signature"
	// ReasonExpired 证书不在有效期内（NotBefore/NotAfter）。
	ReasonExpired Reason = "expired"
	// ReasonNotCA 链中承担签发职责的证书不满足 BasicConstraints CA=true。
	ReasonNotCA Reason = "not_ca"
	// ReasonKeyUsage KeyUsage 不满足要求（CA 缺 keyCertSign、叶子缺 digitalSignature）。
	ReasonKeyUsage Reason = "key_usage_violation"
	// ReasonEKU 链上某层 EKU 未放行目标用途。
	ReasonEKU Reason = "eku_violation"
	// ReasonPathLen 违反 RFC 5280 6.1.4 pathLenConstraint。
	ReasonPathLen Reason = "path_len_violation"
	// ReasonNameConstraint 累积 permitted/excluded NameConstraints 拒绝了某个 SAN。
	ReasonNameConstraint Reason = "name_constraint_violation"
	// ReasonHostname 主机名既不匹配任何 DNS SAN，也不满足 CN 回退条件。
	ReasonHostname Reason = "hostname_violation"
	// ReasonCriticalExtension 出现无法识别且 Critical=true 的扩展。
	ReasonCriticalExtension Reason = "unhandled_critical_extension"
	// ReasonRevoked 证书序列号出现在有效 CRL 中。
	ReasonRevoked Reason = "revoked"
	// ReasonCRL CRL 自身签名无效或不在生效时间窗内。
	ReasonCRL Reason = "crl_invalid"
)

// Attempt 记录一次候选链验证尝试，便于调用方观测回溯过程。
type Attempt struct {
	// Chain 为根在前、叶子在后的候选链。
	Chain []*CertSummary
	// Err 该候选链的验证失败原因；成功的尝试不会出现在 Error.Attempts 中。
	Err *Error
}

// Error 是引擎返回的结构化验证错误。
type Error struct {
	// Reason 失败类别，取值为本包定义的 Reason* 常量。
	Reason Reason
	// Message 人类可读的细节描述。
	Message string
	// Attempts 所有失败候选链的诊断信息（仅顶层错误填充）。
	Attempts []Attempt
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "pathverify: %s: %s", e.Reason, e.Message)
	for _, a := range e.Attempts {
		b.WriteString("\n  - candidate [")
		for i, c := range a.Chain {
			if i > 0 {
				b.WriteString(" -> ")
			}
			b.WriteString(c.Subject)
		}
		b.WriteString("]: ")
		b.WriteString(a.Err.Error())
	}
	return b.String()
}

// Is 支持 errors.Is 按 Reason 比较（例如 errors.Is(err, &Error{Reason: ReasonExpired})）。
func (e *Error) Is(target error) bool {
	other, ok := target.(*Error)
	if !ok {
		return false
	}
	return other.Reason == "" || other.Reason == e.Reason
}

func fail(reason Reason, format string, args ...any) *Error {
	return &Error{Reason: reason, Message: fmt.Sprintf(format, args...)}
}

// IsReason 判断错误是否属于指定 Reason。
func IsReason(err error, reason Reason) bool {
	e, ok := err.(*Error)
	return ok && e.Reason == reason
}
