package netsec

import "errors"

// NTS 校验各阶段返回的哨兵错误。认证类错误统一由 Server 转换为“不回复”。
var (
	// ErrPacketTooShort NTP 报文不足 48 字节的固定头。
	ErrPacketTooShort = errors.New("netsec: ntp packet shorter than 48-byte header")
	// ErrBadAlignment 扩展字段未按 4 字节对齐或长度非法。
	ErrBadAlignment = errors.New("netsec: extension field not 4-byte aligned or has invalid length")
	// ErrUnknownExtension 出现校验包不认识的扩展类型。
	ErrUnknownExtension = errors.New("netsec: unknown extension field type")
	// ErrMissingUniqueIdentifier 报文缺少 Unique Identifier。
	ErrMissingUniqueIdentifier = errors.New("netsec: missing unique identifier extension")
	// ErrUniqueIdentifierOrder Unique Identifier 不是第一个扩展。
	ErrUniqueIdentifierOrder = errors.New("netsec: unique identifier must be the first extension")
	// ErrUniqueIdentifierTooShort Unique Identifier 载荷少于 32 字节。
	ErrUniqueIdentifierTooShort = errors.New("netsec: unique identifier must be at least 32 octets")
	// ErrDuplicateExtension 同类扩展出现次数不符合 NTS 规定（如多个 UID）。
	ErrDuplicateExtension = errors.New("netsec: duplicate extension field")
	// ErrMissingCookie 报文缺少 NTS Cookie。
	ErrMissingCookie = errors.New("netsec: missing nts cookie")
	// ErrCookieOrder Cookie 出现在 Authenticator 之后。
	ErrCookieOrder = errors.New("netsec: nts cookie must precede the authenticator")
	// ErrMissingAuthenticator 报文缺少 NTS Authenticator。
	ErrMissingAuthenticator = errors.New("netsec: missing nts authenticator")
	// ErrAuthenticatorOrder Authenticator 不是最后一个扩展。
	ErrAuthenticatorOrder = errors.New("netsec: nts authenticator must be the last extension")
	// ErrAuthenticatorMalformed Authenticator 字段体结构无法解析。
	ErrAuthenticatorMalformed = errors.New("netsec: malformed nts authenticator")
	// ErrAuthenticationFailed AEAD 校验失败（密钥不匹配或报文被篡改）。
	ErrAuthenticationFailed = errors.New("netsec: nts authenticator verification failed")
	// ErrReplayedRequest Unique Identifier 命中重放缓存。
	ErrReplayedRequest = errors.New("netsec: replayed nts request (unique identifier seen before)")
	// ErrReusedCookie Cookie 在一次性缓存中已被消费。
	ErrReusedCookie = errors.New("netsec: nts cookie has already been used")
	// ErrUnknownCookie 没有任何 Cookie 能被本机解出（密钥轮换或伪造）。
	ErrUnknownCookie = errors.New("netsec: no usable nts cookie")
	// ErrUnsupportedAEAD Cookie/Authenticator 协商了未注册的 AEAD 算法。
	ErrUnsupportedAEAD = errors.New("netsec: unsupported aead algorithm")
	// ErrInvalidKeySize AEAD 密钥长度不被算法接受。
	ErrInvalidKeySize = errors.New("netsec: invalid aead key size")
)
