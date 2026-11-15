// Package netsec 提供可嵌入 TLS 握手后回调的证书校验策略。
//
// 本文件实现 SCT（Signed Certificate Timestamp，RFC 6962）多样性策略：
// 证书内嵌的 SCT 必须达到与证书有效期匹配的数量，并且来自相互独立的
// 日志运营方。日志信任表（LogList）与 SCT 签名验证（SignatureVerifier）
// 均由调用方注入，本组件只负责策略判定，不内置任何具体日志或公钥。
package netsec

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"time"
)

// VersionV1 是 RFC 6962 定义的 SCT v1 版本号（取值 0），其余版本一律不计。
const VersionV1 uint8 = 0

// 策略固定门槛，见题目规定。
const (
	// futureTolerance 是允许的 SCT 时间戳相对验证时间的超前量；
	// 超过该窗口的“未来” SCT 不计入有效集合。
	futureTolerance = 5 * time.Minute
	// longLifetimeBoundary 是证书长短有效期的分界：有效期不超过
	// 180 天按短证书处理，超过 180 天按长证书处理。
	longLifetimeBoundary = 180 * 24 * time.Hour
	// minSCTsShortLifetime 是短有效期证书要求的最少有效 SCT 数。
	minSCTsShortLifetime = 2
	// minSCTsLongLifetime 是长有效期证书要求的最少有效 SCT 数。
	minSCTsLongLifetime = 3
	// minDistinctOperators 是有效 SCT 必须覆盖的最少不同运营方数。
	minDistinctOperators = 2
)

// LogStatus 描述 CT 日志在日志表中的运营状态。
type LogStatus int

const (
	// StatusUnknown 表示日志表未收录该日志。
	StatusUnknown LogStatus = iota
	// StatusQualified 表示日志合格可用（RFC 6962 术语 qualified）。
	StatusQualified
	// StatusUsable 表示日志正常可用。
	StatusUsable
	// StatusReadonly 表示日志只读，不再收录但仍接受其历史 SCT。
	StatusReadonly
	// StatusRetired 表示日志已退休；退休时间之后签发的 SCT 不计。
	StatusRetired
	// StatusRejected 表示日志被拒绝，其 SCT 一律不计。
	StatusRejected
	// StatusPending 表示日志待收录，其 SCT 一律不计。
	StatusPending
)

// Log 描述日志表中的单条 CT 日志。
type Log interface {
	// LogID 返回 RFC 6962 定义的 32 字节日志 ID。
	LogID() []byte
	// Operator 返回运营方标识；相同字符串视为同一运营方。
	Operator() string
	// Status 返回日志当前状态。
	Status() LogStatus
	// RetiredAt 返回退休时间，仅在 Status 为 StatusRetired 时有意义；
	// 零值表示没有可用的退休时间。
	RetiredAt() time.Time
}

// LogList 是调用方注入的日志信任表，按 log_id 查找日志。
type LogList interface {
	// LogByID 返回 log_id 对应的日志；未收录时第二个返回值为 false。
	LogByID(logID []byte) (Log, bool)
}

// MapLogList 是基于 map 的 LogList 实现，方便调用方直接构造或在测试中使用。
// key 为 log_id 的原始字节。
type MapLogList map[string]Log

// LogByID 实现 LogList 接口。
func (m MapLogList) LogByID(logID []byte) (Log, bool) {
	l, ok := m[string(logID)]
	return l, ok
}

// StaticLog 是 Log 的简单值实现，适合固定日志表与测试场景。
type StaticLog struct {
	ID           []byte
	OperatorName string
	LogStatus    LogStatus
	Retired      time.Time
}

// LogID 返回日志 ID。
func (l StaticLog) LogID() []byte { return l.ID }

// Operator 返回运营方标识。
func (l StaticLog) Operator() string { return l.OperatorName }

// Status 返回日志状态。
func (l StaticLog) Status() LogStatus { return l.LogStatus }

// RetiredAt 返回退休时间。
func (l StaticLog) RetiredAt() time.Time { return l.Retired }

// SignatureVerifier 由调用方实现，负责对单份 SCT 的签名做密码学验证。
// 返回 nil 表示签名有效；返回错误表示签名无效，该 SCT 不计入有效集合。
type SignatureVerifier interface {
	VerifySCT(sct *SCT, cert *x509.Certificate) error
}

// SignatureVerifierFunc 让普通函数满足 SignatureVerifier 接口。
type SignatureVerifierFunc func(sct *SCT, cert *x509.Certificate) error

// VerifySCT 实现 SignatureVerifier 接口。
func (f SignatureVerifierFunc) VerifySCT(sct *SCT, cert *x509.Certificate) error {
	return f(sct, cert)
}

// RejectReason 记录一份 SCT 未被计入有效集合的具体原因，便于回调侧审计。
type RejectReason int

const (
	// RejectUnknown 是未分类原因的占位值。
	RejectUnknown RejectReason = iota
	// RejectNotV1 表示 SCT 版本不是 v1。
	RejectNotV1
	// RejectFutureTimestamp 表示时间戳超过验证时间 5 分钟以上。
	RejectFutureTimestamp
	// RejectUnknownLog 表示 log_id 不在日志表中。
	RejectUnknownLog
	// RejectLogRejected 表示日志处于被拒绝状态。
	RejectLogRejected
	// RejectLogPending 表示日志处于待收录状态。
	RejectLogPending
	// RejectRetiredAfterTimestamp 表示日志已退休且 SCT 晚于退休时间签发。
	RejectRetiredAfterTimestamp
	// RejectBadSignature 表示注入的签名验证器判定签名无效。
	RejectBadSignature
	// RejectDuplicateLogID 表示同一 log_id 已有一份 SCT 被处理，去重丢弃。
	RejectDuplicateLogID
)

// SCT 是从证书 SCT 列表扩展中解析出的单份 SCT。DigitallySigned 保留
// RFC 6962 DigitallySigned 结构的原始字节，连同解析出的字段一起交给
// 调用方的 SignatureVerifier 验证。
type SCT struct {
	Version         uint8
	LogID           []byte
	Timestamp       time.Time
	Extensions      []byte
	SignatureHash   uint8
	SignatureAlgo   uint8
	Signature       []byte
	DigitallySigned []byte
}

// RejectedSCT 记录被拒绝的 SCT 及其原因。
type RejectedSCT struct {
	SCT    *SCT
	Reason RejectReason
}

// Result 是一次策略评估的结构化结果。
type Result struct {
	// Accepted 是通过全部检查并计入门槛的 SCT，每个 log_id 至多一份。
	Accepted []*SCT
	// Rejected 记录所有未计入的 SCT 及原因，按处理顺序排列，含重复项。
	Rejected []RejectedSCT
	// RequiredSCTs 是本次评估适用的最少有效 SCT 数量。
	RequiredSCTs int
	// LongLifetime 表示证书有效期超过 180 天。
	LongLifetime bool
}

// Policy 是 SCT 多样性策略组件。当注入的 LogList 与 SignatureVerifier
// 均为只读依赖时，Policy 可被并发使用。
type Policy struct {
	// Logs 是调用方注入的日志信任表，必填。
	Logs LogList
	// Verifier 是调用方注入的 SCT 签名验证器，必填。
	Verifier SignatureVerifier
	// Now 返回验证时间；为 nil 时使用 time.Now，注入固定时间便于测试。
	Now func() time.Time
}

func (p *Policy) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// FailureKind 区分策略失败的类别。
type FailureKind int

const (
	// FailureNoSCT 表示没有任何 SCT 通过检查。
	FailureNoSCT FailureKind = iota
	// FailureNotEnoughSCTs 表示有效 SCT 数量未达门槛。
	FailureNotEnoughSCTs
	// FailureNotEnoughOperators 表示有效 SCT 未覆盖至少两个运营方。
	FailureNotEnoughOperators
)

// PolicyError 是策略不满足时返回的错误。
type PolicyError struct {
	Kind              FailureKind
	Accepted          int
	RequiredSCTs      int
	DistinctOperators int
}

func (e *PolicyError) Error() string {
	switch e.Kind {
	case FailureNoSCT:
		return "sctpolicy: certificate has no valid embedded SCTs"
	case FailureNotEnoughSCTs:
		return "sctpolicy: not enough valid SCTs"
	case FailureNotEnoughOperators:
		return "sctpolicy: accepted SCTs do not span at least two independent operators"
	default:
		return "sctpolicy: policy failure"
	}
}

// 配置与入参错误。
var (
	errNoLogList     = errors.New("sctpolicy: nil LogList injected")
	errNoVerifier    = errors.New("sctpolicy: nil SignatureVerifier injected")
	errNoCertificate = errors.New("sctpolicy: nil certificate")
)

// Evaluate 对给定证书与其 SCT 列表执行完整策略，返回逐项判定结果，
// 不做最终通过/失败结论。校验规则（按处理顺序）：
//   - SCT 版本必须为 v1；
//   - 时间戳不得晚于验证时间 5 分钟以上；
//   - log_id 必须在日志表中，且日志状态不得为 rejected/pending；
//   - retired 日志在其退休时间之后签发的 SCT 不计；
//   - 签名必须通过注入的 SignatureVerifier；
//   - 同一 log_id 只计一份，重复项记 RejectDuplicateLogID。
func (p *Policy) Evaluate(cert *x509.Certificate, scts []*SCT) (*Result, error) {
	if p.Logs == nil {
		return nil, errNoLogList
	}
	if p.Verifier == nil {
		return nil, errNoVerifier
	}
	if cert == nil {
		return nil, errNoCertificate
	}

	result := &Result{
		LongLifetime: cert.NotAfter.Sub(cert.NotBefore) > longLifetimeBoundary,
	}
	if result.LongLifetime {
		result.RequiredSCTs = minSCTsLongLifetime
	} else {
		result.RequiredSCTs = minSCTsShortLifetime
	}

	now := p.now()
	seenLogIDs := make(map[string]struct{})

	for _, sct := range scts {
		if sct == nil {
			continue
		}
		if sct.Version != VersionV1 {
			result.Rejected = append(result.Rejected, RejectedSCT{sct, RejectNotV1})
			continue
		}
		if sct.Timestamp.After(now.Add(futureTolerance)) {
			result.Rejected = append(result.Rejected, RejectedSCT{sct, RejectFutureTimestamp})
			continue
		}

		log, ok := p.Logs.LogByID(sct.LogID)
		if !ok || log == nil {
			result.Rejected = append(result.Rejected, RejectedSCT{sct, RejectUnknownLog})
			continue
		}
		switch log.Status() {
		case StatusRejected:
			result.Rejected = append(result.Rejected, RejectedSCT{sct, RejectLogRejected})
			continue
		case StatusPending:
			result.Rejected = append(result.Rejected, RejectedSCT{sct, RejectLogPending})
			continue
		case StatusRetired:
			if retired := log.RetiredAt(); !retired.IsZero() && sct.Timestamp.After(retired) {
				result.Rejected = append(result.Rejected, RejectedSCT{sct, RejectRetiredAfterTimestamp})
				continue
			}
		}

		if err := p.Verifier.VerifySCT(sct, cert); err != nil {
			result.Rejected = append(result.Rejected, RejectedSCT{sct, RejectBadSignature})
			continue
		}

		key := string(sct.LogID)
		if _, dup := seenLogIDs[key]; dup {
			result.Rejected = append(result.Rejected, RejectedSCT{sct, RejectDuplicateLogID})
			continue
		}
		seenLogIDs[key] = struct{}{}
		result.Accepted = append(result.Accepted, sct)
	}

	return result, nil
}

// DistinctOperators 统计结果中有效 SCT 覆盖的不同运营方数量。
func (p *Policy) DistinctOperators(result *Result) int {
	operators := make(map[string]struct{})
	for _, sct := range result.Accepted {
		log, ok := p.Logs.LogByID(sct.LogID)
		if !ok || log == nil {
			continue
		}
		operators[log.Operator()] = struct{}{}
	}
	return len(operators)
}

// VerifyCertificate 对证书及其已解析的 SCT 列表应用策略并给出最终结论。
// 通过时返回 nil；策略不满足时返回 *PolicyError，配置错误返回普通 error。
func (p *Policy) VerifyCertificate(cert *x509.Certificate, scts []*SCT) error {
	result, err := p.Evaluate(cert, scts)
	if err != nil {
		return err
	}
	if len(result.Accepted) == 0 {
		return &PolicyError{Kind: FailureNoSCT}
	}
	operators := p.DistinctOperators(result)
	if len(result.Accepted) < result.RequiredSCTs {
		return &PolicyError{
			Kind:              FailureNotEnoughSCTs,
			Accepted:          len(result.Accepted),
			RequiredSCTs:      result.RequiredSCTs,
			DistinctOperators: operators,
		}
	}
	if operators < minDistinctOperators {
		return &PolicyError{
			Kind:              FailureNotEnoughOperators,
			Accepted:          len(result.Accepted),
			RequiredSCTs:      result.RequiredSCTs,
			DistinctOperators: operators,
		}
	}
	return nil
}

// VerifyEmbeddedSCTs 解析证书内嵌的 RFC 6962 SCT 列表扩展
// （OID 1.3.6.1.4.1.11129.2.4.2），再应用策略。证书未携带该扩展时
// 按无有效 SCT 处理并返回 FailureNoSCT。
func (p *Policy) VerifyEmbeddedSCTs(cert *x509.Certificate) error {
	if cert == nil {
		return errNoCertificate
	}
	scts, err := ExtractEmbeddedSCTs(cert)
	if err != nil {
		return err
	}
	return p.VerifyCertificate(cert, scts)
}

// VerifyConnection 供基于 crypto/tls ConnectionState 的握手后回调使用，
// 校验对端叶子证书（state.PeerCertificates[0]）内嵌的 SCT。
func (p *Policy) VerifyConnection(state tls.ConnectionState) error {
	if len(state.PeerCertificates) == 0 {
		return errNoCertificate
	}
	return p.VerifyEmbeddedSCTs(state.PeerCertificates[0])
}

// VerifyPeerCertificate 适配 tls.Config.VerifyPeerCertificate 回调签名。
// rawCerts 为 DER 形式的证书链；verifiedChains 可能为空（例如
// InsecureSkipVerify 场景），本策略只依赖叶子证书内嵌的 SCT。
func (p *Policy) VerifyPeerCertificate(rawCerts [][]byte, _ [][]*x509.Certificate) error {
	if len(rawCerts) == 0 {
		return errNoCertificate
	}
	cert, err := x509.ParseCertificate(rawCerts[0])
	if err != nil {
		return err
	}
	return p.VerifyEmbeddedSCTs(cert)
}
