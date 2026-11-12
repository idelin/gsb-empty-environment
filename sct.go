package netsec

import (
	"crypto/x509"
	"errors"
	"time"
)

// SCTVersion 是 RFC 6962 定义的 SCT 协议版本。
type SCTVersion uint8

const (
	// SCTVersionV1 是本策略唯一接受的 SCT 版本。
	SCTVersionV1 SCTVersion = 0
)

const (
	// MaxSCTClockSkew 是允许的 SCT 时间戳相对验证时间的最大前漂。
	// 时间戳晚于验证时间超过该时长的 SCT 不计为有效 SCT。
	MaxSCTClockSkew = 5 * time.Minute

	// MaxShortLivedValidity 是“短期证书”的有效期边界。
	// 有效期不超过 180 天的证书至少需要 2 份有效 SCT，
	// 超过 180 天的证书至少需要 3 份。
	MaxShortLivedValidity = 180 * 24 * time.Hour

	// MinDistinctOperators 是有效 SCT 必须覆盖的不同运营方数量。
	MinDistinctOperators = 2
)

// LogStatus 描述日志表中某条 CT 日志的收录状态。
type LogStatus int

const (
	// LogStatusQualified 表示日志处于正常收录状态。
	LogStatusQualified LogStatus = iota
	// LogStatusRetired 表示日志已退休。退休前签发的 SCT 仍然有效，
	// SCT 时间戳晚于退休时间的不计。
	LogStatusRetired
	// LogStatusRejected 表示日志已被拒绝，其 SCT 一律不计。
	LogStatusRejected
	// LogStatusPending 表示日志尚待收录，其 SCT 一律不计。
	LogStatusPending
)

// String 返回日志状态的可读名称。
func (s LogStatus) String() string {
	switch s {
	case LogStatusQualified:
		return "qualified"
	case LogStatusRetired:
		return "retired"
	case LogStatusRejected:
		return "rejected"
	case LogStatusPending:
		return "pending"
	default:
		return "unknown"
	}
}

// SCT 是一份签名证书时间戳（RFC 6962 第 3.2 节）。
type SCT struct {
	// Version 是 SCT 协议版本，只有 SCTVersionV1 才被接受。
	Version SCTVersion
	// LogID 是签发该 SCT 的 CT 日志的 32 字节标识。
	LogID [32]byte
	// Timestamp 是 SCT 中的毫秒级日志时间戳。
	Timestamp time.Time
	// Extensions 是日志随 SCT 返回的原始扩展字节。
	Extensions []byte
	// Signature 是 RFC 6962 DigitallySigned 结构的完整原始字节
	// （hash_algorithm、sig_algorithm 及 u16 长度前缀的签名值），
	// 由注入的签名验证器解析并校验。
	Signature []byte
}

// LogInfo 是日志表中单条日志的元数据。
type LogInfo struct {
	// Operator 标识日志运营方。同一运营方运营的多条日志不增加多样性。
	Operator string
	// Status 是日志当前的收录状态。
	Status LogStatus
	// RetiredAt 仅在 Status 为 LogStatusRetired 时有意义，
	// 表示日志退休时间；晚于该时间签发的 SCT 不计。
	RetiredAt time.Time
}

// LogTable 根据 log_id 查询日志元数据。
// 未知日志返回 (零值, false)，对应 SCT 不计为有效 SCT。
// 具体实现由调用方注入。
type LogTable interface {
	LogInfo(logID [32]byte) (LogInfo, bool)
}

// LogTableFunc 让普通函数满足 LogTable 接口。
type LogTableFunc func(logID [32]byte) (LogInfo, bool)

// LogInfo 实现 LogTable。
func (f LogTableFunc) LogInfo(logID [32]byte) (LogInfo, bool) {
	return f(logID)
}

// SignatureVerifier 验证 SCT 签名。
// 证书、SCT 与日志元数据由策略传入，公钥等验证材料由实现自行解析管理。
// 返回 nil 表示签名有效；具体实现由调用方注入。
type SignatureVerifier interface {
	VerifySCTSignature(cert *x509.Certificate, sct *SCT, log LogInfo) error
}

// SignatureVerifierFunc 让普通函数满足 SignatureVerifier 接口。
type SignatureVerifierFunc func(cert *x509.Certificate, sct *SCT, log LogInfo) error

// VerifySCTSignature 实现 SignatureVerifier。
func (f SignatureVerifierFunc) VerifySCTSignature(cert *x509.Certificate, sct *SCT, log LogInfo) error {
	return f(cert, sct, log)
}

// RejectReason 解释单份 SCT 未被计为有效 SCT 的原因。
type RejectReason int

const (
	// RejectNone 表示该 SCT 被计为有效 SCT。
	RejectNone RejectReason = iota
	// RejectDuplicate 表示同一 log_id 已有一份有效 SCT，本次只计一份。
	RejectDuplicate
	// RejectUnsupportedVersion 表示 SCT 版本不是 v1。
	RejectUnsupportedVersion
	// RejectFutureTimestamp 表示 SCT 时间戳晚于验证时间超过 5 分钟。
	RejectFutureTimestamp
	// RejectUnknownLog 表示 log_id 不在日志表中。
	RejectUnknownLog
	// RejectLogRejected 表示日志处于被拒绝状态。
	RejectLogRejected
	// RejectLogPending 表示日志处于待收录状态。
	RejectLogPending
	// RejectRetiredTimestamp 表示日志已退休且 SCT 时间戳晚于退休时间。
	RejectRetiredTimestamp
	// RejectBadSignature 表示 SCT 签名验证失败。
	RejectBadSignature
)

// String 返回拒绝原因的可读名称。
func (r RejectReason) String() string {
	switch r {
	case RejectNone:
		return "accepted"
	case RejectDuplicate:
		return "duplicate"
	case RejectUnsupportedVersion:
		return "unsupported_version"
	case RejectFutureTimestamp:
		return "future_timestamp"
	case RejectUnknownLog:
		return "unknown_log"
	case RejectLogRejected:
		return "log_rejected"
	case RejectLogPending:
		return "log_pending"
	case RejectRetiredTimestamp:
		return "retired_timestamp"
	case RejectBadSignature:
		return "bad_signature"
	default:
		return "unknown"
	}
}

// SCTResult 记录单份 SCT 的判定结果。
type SCTResult struct {
	// SCT 是被判定的 SCT。
	SCT *SCT
	// LogID 是该 SCT 的 log_id 副本，便于不深入 SCT 结构即可读取。
	LogID [32]byte
	// Accepted 表示该 SCT 是否被计为有效 SCT。
	Accepted bool
	// Reason 在 Accepted 为 false 时给出拒绝原因。
	Reason RejectReason
}

// SCTReport 是一次策略评估的结构化结果，供回调记录或上报。
type SCTReport struct {
	// RequiredCount 是按证书有效期确定的有效 SCT 数量门槛。
	RequiredCount int
	// AcceptedCount 是去重后实际计入的有效 SCT 数量。
	AcceptedCount int
	// DistinctOperators 是有效 SCT 覆盖的不同运营方数量。
	DistinctOperators int
	// Results 与传入的 SCT 切片一一对应（含重复项）。
	Results []SCTResult
}

// 策略不满足时返回的哨兵错误，可用 errors.Is 判定。
var (
	// ErrTooFewSCTs 表示有效 SCT 数量未达到有效期对应的门槛。
	ErrTooFewSCTs = errors.New("sct policy: not enough valid SCTs")
	// ErrTooFewOperators 表示有效 SCT 未覆盖至少两个不同运营方。
	ErrTooFewOperators = errors.New("sct policy: SCTs do not span enough distinct operators")
)

// PolicyErrorCode 区分策略失败的类别。
type PolicyErrorCode int

const (
	// PolicyErrorTooFewSCTs 对应 ErrTooFewSCTs。
	PolicyErrorTooFewSCTs PolicyErrorCode = iota
	// PolicyErrorTooFewOperators 对应 ErrTooFewOperators。
	PolicyErrorTooFewOperators
)

// PolicyError 在策略不满足时携带完整报告，供握手后回调返回或记录。
type PolicyError struct {
	// Code 是失败类别。
	Code PolicyErrorCode
	// Report 是完整的评估报告。
	Report *SCTReport
}

// Error 实现 error。
func (e *PolicyError) Error() string {
	switch e.Code {
	case PolicyErrorTooFewSCTs:
		return ErrTooFewSCTs.Error()
	case PolicyErrorTooFewOperators:
		return ErrTooFewOperators.Error()
	default:
		return "sct policy: unknown failure"
	}
}

// Unwrap 让 errors.Is 能匹配对应的哨兵错误。
func (e *PolicyError) Unwrap() error {
	switch e.Code {
	case PolicyErrorTooFewSCTs:
		return ErrTooFewSCTs
	case PolicyErrorTooFewOperators:
		return ErrTooFewOperators
	default:
		return nil
	}
}

// SCTPolicy 是供握手后回调使用的嵌入式 SCT 策略组件。
// 签名验证与日志表均由调用方注入；策略本身只负责规则判定。
type SCTPolicy struct {
	// Logs 提供 log_id 到日志元数据的查询，必填。
	Logs LogTable
	// Verifier 负责验证 SCT 签名，必填。
	Verifier SignatureVerifier
	// Now 返回验证时间；为 nil 时使用 time.Now。
	// 注入固定时钟便于测试与确定性校验。
	Now func() time.Time
}

func (p *SCTPolicy) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// RequiredSCTCount 按证书有效期返回有效 SCT 数量门槛：
// 有效期不超过 180 天返回 2，超过 180 天返回 3。
// 边界恰好 180 天按“不超过”处理，门槛为 2。
func RequiredSCTCount(cert *x509.Certificate) int {
	if cert.NotAfter.Sub(cert.NotBefore) > MaxShortLivedValidity {
		return 3
	}
	return 2
}

// Evaluate 按本题规定对证书嵌入的 SCT 执行策略判定。
//
// 规则依次为：SCT 版本必须为 v1；时间戳不得晚于验证时间 5 分钟以上；
// log_id 必须存在于日志表；待收录、被拒绝的日志不计；已退休日志在
// 退休时间之后签发的 SCT 不计；签名必须通过；同一 log_id 只计一份。
// 去重后，有效期不超过 180 天的证书至少需要 2 份有效 SCT，
// 超过 180 天的至少 3 份，且有效 SCT 须来自至少两个不同运营方。
//
// 策略满足时返回报告和 nil；不满足时返回报告和 *PolicyError。
func (p *SCTPolicy) Evaluate(cert *x509.Certificate, scts []*SCT) (*SCTReport, error) {
	if cert == nil {
		return nil, errors.New("sct policy: nil certificate")
	}
	if p == nil || p.Logs == nil {
		return nil, errors.New("sct policy: missing log table")
	}
	if p.Verifier == nil {
		return nil, errors.New("sct policy: missing signature verifier")
	}

	verifyTime := p.now()
	report := &SCTReport{
		RequiredCount: RequiredSCTCount(cert),
		Results:       make([]SCTResult, len(scts)),
	}

	// acceptedByLog 只记录已通过全部校验的 SCT，从而保证同一 log_id 只计一份。
	acceptedByLog := make(map[[32]byte]struct{})
	operators := make(map[string]struct{})

	for i, sct := range scts {
		result := SCTResult{SCT: sct}
		if sct != nil {
			result.LogID = sct.LogID
		}

		reason := p.evaluateOne(cert, sct, verifyTime, acceptedByLog)
		result.Reason = reason
		result.Accepted = reason == RejectNone
		if result.Accepted {
			acceptedByLog[sct.LogID] = struct{}{}
			report.AcceptedCount++
			if info, ok := p.Logs.LogInfo(sct.LogID); ok {
				operators[info.Operator] = struct{}{}
			}
		}
		report.Results[i] = result
	}

	report.DistinctOperators = len(operators)

	if report.AcceptedCount < report.RequiredCount {
		return report, &PolicyError{Code: PolicyErrorTooFewSCTs, Report: report}
	}
	if report.DistinctOperators < MinDistinctOperators {
		return report, &PolicyError{Code: PolicyErrorTooFewOperators, Report: report}
	}
	return report, nil
}

func (p *SCTPolicy) evaluateOne(cert *x509.Certificate, sct *SCT, verifyTime time.Time, acceptedByLog map[[32]byte]struct{}) RejectReason {
	if sct == nil {
		return RejectUnknownLog
	}
	if sct.Version != SCTVersionV1 {
		return RejectUnsupportedVersion
	}
	if sct.Timestamp.After(verifyTime.Add(MaxSCTClockSkew)) {
		return RejectFutureTimestamp
	}

	info, ok := p.Logs.LogInfo(sct.LogID)
	if !ok {
		return RejectUnknownLog
	}
	switch info.Status {
	case LogStatusRejected:
		return RejectLogRejected
	case LogStatusPending:
		return RejectLogPending
	case LogStatusRetired:
		// 退休时间之后签发的 SCT 不计；退休时间点本身及更早的 SCT 仍可计入。
		if sct.Timestamp.After(info.RetiredAt) {
			return RejectRetiredTimestamp
		}
	case LogStatusQualified:
	default:
		return RejectUnknownLog
	}

	if err := p.Verifier.VerifySCTSignature(cert, sct, info); err != nil {
		return RejectBadSignature
	}

	// 重复的 SCT（同一 log_id 已有一份有效 SCT）不重复计数。
	if _, dup := acceptedByLog[sct.LogID]; dup {
		return RejectDuplicate
	}
	return RejectNone
}
