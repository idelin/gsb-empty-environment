package clientassertion

import "errors"

// 可通过 errors.Is 精确匹配的校验失败原因。
var (
	// ErrMultipleCredentials 表示同一请求同时携带 client_secret 与 client_assertion。
	ErrMultipleCredentials = errors.New("clientassertion: request must not contain both client_secret and client_assertion")
	// ErrMissingAssertion 表示 private_key_jwt 流程缺少 client_assertion。
	ErrMissingAssertion = errors.New("clientassertion: missing client_assertion")
	// ErrUnsupportedAssertionType 表示 client_assertion_type 不是 JWT bearer 类型。
	ErrUnsupportedAssertionType = errors.New("clientassertion: unsupported client_assertion_type")
	// ErrMissingClientID 表示缺少 client_id。
	ErrMissingClientID = errors.New("clientassertion: missing client_id")
	// ErrMalformedAssertion 表示断言不是合法的三段式 JWT 或编码/JSON 非法。
	ErrMalformedAssertion = errors.New("clientassertion: malformed client_assertion")
	// ErrDisallowedAlgorithm 表示 alg 缺失、不在调用方白名单内，或属于 none/HS 系列。
	ErrDisallowedAlgorithm = errors.New("clientassertion: JWT alg is missing or not allowed")
	// ErrInvalidIssuer 表示 iss 缺失或不等于 client_id。
	ErrInvalidIssuer = errors.New("clientassertion: iss must equal client_id")
	// ErrInvalidSubject 表示 sub 缺失或不等于 client_id。
	ErrInvalidSubject = errors.New("clientassertion: sub must equal client_id")
	// ErrInvalidAudience 表示 aud 不是单个字符串、不是本服务器 issuer，或使用了数组形式。
	ErrInvalidAudience = errors.New("clientassertion: aud must be a single string equal to the server issuer")
	// ErrMissingExpiration 表示 exp 缺失或不是合法数值。
	ErrMissingExpiration = errors.New("clientassertion: exp is required")
	// ErrMissingIssuedAt 表示 iat 缺失或不是合法数值。
	ErrMissingIssuedAt = errors.New("clientassertion: iat is required")
	// ErrTokenExpired 表示当前时间已达到 exp。
	ErrTokenExpired = errors.New("clientassertion: assertion has expired")
	// ErrLifetimeTooLong 表示 exp-iat 超过 300 秒。
	ErrLifetimeTooLong = errors.New("clientassertion: assertion lifetime must not exceed 300 seconds")
	// ErrIssuedAtInFuture 表示 iat 比当前时间晚 30 秒以上。
	ErrIssuedAtInFuture = errors.New("clientassertion: iat is too far in the future")
	// ErrMissingJTI 表示 jti 缺失或为空。
	ErrMissingJTI = errors.New("clientassertion: jti is required")
	// ErrReplayedJTI 表示同一 client_id 下该 jti 在 exp 之前已被使用过。
	ErrReplayedJTI = errors.New("clientassertion: jti has already been used")
	// ErrSignatureRejected 表示调用方的签名验证回调拒绝了该断言。
	ErrSignatureRejected = errors.New("clientassertion: signature verification failed")
)
