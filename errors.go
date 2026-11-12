package netsec

import "errors"

// 决策结果。
const (
	// ActionPending 尚不能做出接纳决定（观察未满、证据不一致或校验失败）。
	ActionPending = "pending"
	// ActionReplace 用记录集中的新 DS 替换当前 DS。
	ActionReplace = "replace"
	// ActionDelete 接纳删除信号 0 0 0 00，移除当前 DS。
	ActionDelete = "delete"
)

// 失败规则标识，供导入方做指标计数与审计分类。
const (
	RuleMalformed           = "malformed"
	RuleRRSIGUnsigned       = "rrset_unsigned"
	RuleRRSIGInvalid        = "rrset_signature_invalid"
	RuleRRSIGTime           = "rrset_time_window"
	RuleRRSIGSignerMismatch = "rrset_signer_mismatch"
	RuleSignerNotKSK        = "signer_not_ksk"
	RuleSignerNotAnchored   = "signer_not_ds_anchored"
	RuleNewDSNoDNSKEY       = "new_ds_without_dnskey"
	RuleDeleteMixed         = "delete_signal_mixed"
	RuleDeleteMalformed     = "delete_signal_malformed"
	RuleCDSCDNSKEYMismatch  = "cds_cdnskey_mismatch"
	RuleServerDivergence    = "server_set_divergence"
	RuleObservationGap      = "observation_gap"
	RuleObservationShort    = "observation_too_short"
	RuleConfig              = "config"
)

// RuleError 携带机器可读的失败规则标识，导入方可用 errors.Is 匹配
// 具体规则（如 ErrSignerNotAnchored），也可用 Rule 字段做统计。
type RuleError struct {
	Rule    string
	Message string
	cause   error
}

func (e *RuleError) Error() string {
	if e.cause != nil {
		return "netsec: " + e.Rule + ": " + e.Message + ": " + e.cause.Error()
	}
	return "netsec: " + e.Rule + ": " + e.Message
}

func (e *RuleError) Unwrap() error { return e.cause }

func ruleErr(rule, msg string, cause error) *RuleError {
	return &RuleError{Rule: rule, Message: msg, cause: cause}
}

// 可直接用 errors.Is 比较的哨兵错误。
var (
	// ErrMalformed 记录或报文结构不合法。
	ErrMalformed = errors.New("malformed record or message")
	// ErrRRSIGUnsigned RRset 缺少 RRSIG。
	ErrRRSIGUnsigned = errors.New("rrset is not signed")
	// ErrRRSIGInvalid RRSIG 密码学校验失败。
	ErrRRSIGInvalid = errors.New("rrset signature verification failed")
	// ErrRRSIGTime RRSIG 不在有效期内。
	ErrRRSIGTime = errors.New("rrset outside validity period")
	// ErrRRSIGSignerMismatch RRSIG 的 signer/type-covered/labels 与 RRset 不符。
	ErrRRSIGSignerMismatch = errors.New("rrset signer name or covered type mismatch")
	// ErrSignerNotKSK 签名 DNSKEY 未置 SEP 位，不是 KSK。
	ErrSignerNotKSK = errors.New("signing dnskey is not a KSK (no SEP bit)")
	// ErrSignerNotAnchored 签名 KSK 不被父区当前任一 DS 指向。
	ErrSignerNotAnchored = errors.New("signing KSK is not covered by a current DS")
	// ErrNewDSNoDNSKEY 新 DS 在子区已发布的 DNSKEY 集中找不到对应 KSK。
	ErrNewDSNoDNSKEY = errors.New("new DS has no matching published DNSKEY")
	// ErrDeleteMixed 删除记录与其他记录混放。
	ErrDeleteMixed = errors.New("delete signal mixed with other records")
	// ErrDeleteMalformed 删除信号不是规范的 0 0 0 00。
	ErrDeleteMalformed = errors.New("malformed delete signal")
	// ErrCDSCDNSKEYMismatch 同时存在的 CDS 与 CDNSKEY 不能互相推导对应。
	ErrCDSCDNSKEYMismatch = errors.New("CDS and CDNSKEY sets do not correspond")
	// ErrServerDivergence 各权威服务器返回的记录集不一致。
	ErrServerDivergence = errors.New("authoritative servers returned divergent record sets")
	// ErrObservationGap 连续观察中断（出现失败轮或间隔超过 PollInterval）。
	ErrObservationGap = errors.New("continuous observation interrupted")
	// ErrObservationShort 一致观察尚未达到配置的小时数。
	ErrObservationShort = errors.New("observation window not yet satisfied")
	// ErrConfig 配置不合法。
	ErrConfig = errors.New("invalid configuration")
)
