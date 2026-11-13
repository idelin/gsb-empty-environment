package netsec

import "errors"

// 委托链校验过程中可能返回的哨兵错误。资源服务可用 errors.Is 精确判别。
var (
	// ErrEmptyChain 证明链为空，没有任何可校验的凭证。
	ErrEmptyChain = errors.New("netsec: delegation chain is empty")

	// ErrMalformedToken 凭证格式非法（编码损坏或字段缺失）。
	ErrMalformedToken = errors.New("netsec: malformed token")

	// ErrUnsupportedAlgorithm 凭证使用了本库不支持的签名算法。
	ErrUnsupportedAlgorithm = errors.New("netsec: unsupported signature algorithm")

	// ErrInvalidSignature 签名验证失败，凭证可能被篡改或签发者密钥不匹配。
	ErrInvalidSignature = errors.New("netsec: invalid signature")

	// ErrAudienceMismatch 相邻两跳受众/签发者没有首尾相接。
	ErrAudienceMismatch = errors.New("netsec: audience chain mismatch")

	// ErrExpired 凭证在给定时间点已经过期。
	ErrExpired = errors.New("netsec: token expired")

	// ErrExpiryExtended 后一跳的过期时间晚于前一跳，违反只减不增原则。
	ErrExpiryExtended = errors.New("netsec: expiry must not be extended by a delegate")

	// ErrCapabilityEscalated 后一跳的能力超出了前一跳的授权范围。
	ErrCapabilityEscalated = errors.New("netsec: capability escalated beyond parent delegation")

	// ErrUntrustedRoot 链的根签发者不在资源服务信任列表内。
	ErrUntrustedRoot = errors.New("netsec: root issuer is not trusted")

	// ErrCapabilityNotDelegated 所请求的能力没有被证明链中的任何授权覆盖。
	ErrCapabilityNotDelegated = errors.New("netsec: requested capability is not delegated")
)
