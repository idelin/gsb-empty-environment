package jar

import "errors"

// 哨兵错误，调用方可通过 errors.Is 精确匹配安全事件类型，
// 也可以通过 *ValidationError 取得细节信息。
var (
	// ErrDuplicateParameter 表示查询串中某个 OAuth 参数出现了多次。
	ErrDuplicateParameter = errors.New("jar: duplicate authorization request parameter")
	// ErrMissingClientID 表示请求中缺少 client_id。
	ErrMissingClientID = errors.New("jar: missing client_id")
	// ErrRequestURINotSupported 表示使用了 request_uri（本实现不提供拉取能力）。
	ErrRequestURINotSupported = errors.New("jar: request_uri is not supported in-process")
	// ErrBothRequestAndRequestURI 表示同时携带 request 与 request_uri。
	ErrBothRequestAndRequestURI = errors.New("jar: request and request_uri must not be used together")
	// ErrPlaintextDowngrade 表示强模式下缺失 request，试图降级为明文参数。
	ErrPlaintextDowngrade = errors.New("jar: plaintext authorization request rejected: signed request object required")
	// ErrUnknownClient 表示无法在注册表中找到 client_id 对应的客户端。
	ErrUnknownClient = errors.New("jar: unknown or unregistered client_id")
	// ErrMalformedJWT 表示 request JWT 的紧凑序列化结构不合法。
	ErrMalformedJWT = errors.New("jar: malformed request object JWT")
	// ErrAlgorithmNotAllowed 表示 alg 不在策略白名单内（含 none/对称算法）。
	ErrAlgorithmNotAllowed = errors.New("jar: JWT alg is not in the asymmetric allowlist")
	// ErrInvalidTyp 表示保护头 typ 不是 oauth-authz-req+jwt。
	ErrInvalidTyp = errors.New("jar: JWT typ must be exactly \"oauth-authz-req+jwt\"")
	// ErrMissingKeyID 表示非对称密钥需要 kid 才能定位。
	ErrMissingKeyID = errors.New("jar: JWT kid header is required for multi-key clients")
	// ErrKeyNotFound 表示注册表中没有与 kid 匹配的公钥。
	ErrKeyNotFound = errors.New("jar: no public key registered for kid")
	// ErrSignatureInvalid 表示签名验签失败。
	ErrSignatureInvalid = errors.New("jar: request object signature verification failed")
	// ErrNestedRequest 表示 JWT 载荷内嵌了递归 request/request_uri 声明。
	ErrNestedRequest = errors.New("jar: nested request/request_uri claims are forbidden inside the request object")
	// ErrInvalidClaim 表示注册声明（iss/aud/exp/nbf/iat/jti 等）不合法。
	ErrInvalidClaim = errors.New("jar: invalid request object claim")
	// ErrLifetimeExceeded 表示请求对象有效期超过策略上限。
	ErrLifetimeExceeded = errors.New("jar: request object lifetime exceeds policy maximum")
	// ErrReplayedJTI 表示 jti 已被使用（重放攻击）。
	ErrReplayedJTI = errors.New("jar: request object jti has already been used (replay)")
	// ErrOuterInnerConflict 表示外层未签名参数与 JWT 内层受保护参数冲突。
	ErrOuterInnerConflict = errors.New("jar: outer unsigned parameter conflicts with or duplicates the signed request object")
)

// ValidationError 包装校验失败的具体原因，Unwrap 后可匹配对应哨兵错误。
type ValidationError struct {
	Op  string // 失败发生的阶段，例如 "verify_signature"
	Msg string // 人类可读细节
	Err error  // 被包装的哨兵错误
}

func (e *ValidationError) Error() string {
	if e.Msg == "" {
		return e.Op + ": " + e.Err.Error()
	}
	return "jar: " + e.Op + ": " + e.Msg
}

// Unwrap 暴露内部哨兵错误以支持 errors.Is/errors.As。
func (e *ValidationError) Unwrap() error { return e.Err }

func wrapError(op string, sentinel error, format string, args ...any) *ValidationError {
	return &ValidationError{
		Op:  op,
		Err: sentinel,
		Msg: sprintf(format, args...),
	}
}
