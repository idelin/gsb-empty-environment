// Package jar 实现 RFC 9101 定义的 JWT-Secured Authorization Request（JAR）
// 授权请求对象的严格解析与防降级校验。
//
// 本包只依赖 Go 标准库（crypto/ecdsa、crypto/rsa、crypto/sha256 等），
// 不发起任何网络请求，request_uri 拉取不在进程内处理。
//
// 安全保证概览：
//   - 强模式下硬拒绝缺失 request 的明文授权请求，硬拒绝 request/request_uri 混用；
//   - 有效业务参数只取自已验签的 JWT 载荷，外层未签名参数冲突即硬拒；
//   - 仅接受白名单非对称 alg，硬拒 none 与 HS* 对称算法；
//   - 强制 typ=oauth-authz-req+jwt，防止与 ID Token/Access Token 类型混淆；
//   - 强制 iss=client_id、aud 精确匹配本授权服务器 Issuer；
//   - exp/nbf/iat 严格时间窗与最大有效期，jti 原子防重放；
//   - 硬拒绝载荷内嵌递归 request/request_uri 声明。
package jar

import (
	"net/http"
	"net/url"
	"time"
)

// 传输层参数：它们不参与“内层业务参数”比对。
var transportParams = map[string]struct{}{
	"request":     {},
	"request_uri": {},
}

// 受保护业务参数：在外层出现时必须与 JWT 内层精确一致，否则硬拒。
// 有效取值永远来自内层已验签载荷。
var protectedParams = []string{
	"redirect_uri",
	"scope",
	"state",
	"nonce",
	"code_challenge",
}

// Policy 是授权服务器侧的 JAR 强制策略。
type Policy struct {
	// Issuer 本授权服务器 Issuer，aud 必须与其精确相等。
	Issuer string
	// RequireSignedRequestObject 服务端全局强制 JAR 开关。
	RequireSignedRequestObject bool
	// AllowedAlgs 非对称 alg 白名单；为空时使用 DefaultAllowedAlgs。
	AllowedAlgs []string
	// MaxLifetime 请求对象有效期上限（exp 相对 nbf/iat），零值取 300 秒。
	MaxLifetime time.Duration
	// Leeway 时钟偏移容忍量，零值取 30 秒。
	Leeway time.Duration
}

// AuthorizationRequest 是校验通过后的可信授权请求。
// Signed=false 仅在服务端与客户端策略都允许明文时才可能出现。
type AuthorizationRequest struct {
	// Signed 为 true 表示参数来自已验签的 request JWT。
	Signed bool

	ClientID      string
	RedirectURI   string
	Scope         string
	State         string
	Nonce         string
	CodeChallenge string

	// JTI 仅签名请求存在，用于审计与防重放。
	JTI string

	// Params 是全部可信业务参数（严格取自 JWT 内层或经策略放行的明文）。
	Params map[string]string
}

// Validator 是并发安全的 JAR 校验器，可被多个 http.Handler 共享。
type Validator struct {
	policy Policy
	store  ClientStore
	replay ReplayCache
	now    func() time.Time
}

// NewValidator 构造校验器并补齐策略默认值。
func NewValidator(policy Policy, store ClientStore, replay ReplayCache) *Validator {
	if len(policy.AllowedAlgs) == 0 {
		policy.AllowedAlgs = append([]string(nil), DefaultAllowedAlgs...)
	}
	if policy.MaxLifetime <= 0 {
		policy.MaxLifetime = 300 * time.Second
	}
	if policy.Leeway <= 0 {
		policy.Leeway = 30 * time.Second
	}
	if replay == nil {
		replay = NewMemoryReplayCache()
	}
	return &Validator{
		policy: policy,
		store:  store,
		replay: replay,
		now:    time.Now,
	}
}

// ParseHTTPRequest 从 HTTP 请求的 URL 查询串解析授权请求。
func (v *Validator) ParseHTTPRequest(req *http.Request) (*AuthorizationRequest, error) {
	return v.ParseQuery(req.URL.Query())
}

// ParseQuery 从 url.Values 解析授权请求并执行全部 JAR 安全校验。
// 重复参数、明文降级、内外层冲突、声明非法或重放都会返回 *ValidationError。
func (v *Validator) ParseQuery(values url.Values) (*AuthorizationRequest, error) {
	outer, err := flattenValues(values)
	if err != nil {
		return nil, err
	}

	_, hasRequestURI := outer["request_uri"]
	requestJWT, hasRequest := outer["request"]

	// 门禁 1：request 与 request_uri 互斥，request_uri 进程内一律不支持。
	if hasRequest && hasRequestURI {
		return nil, wrapError("gate", ErrBothRequestAndRequestURI, "request and request_uri must not be combined")
	}
	if hasRequestURI {
		return nil, wrapError("gate", ErrRequestURINotSupported, "in-process request_uri retrieval is disabled")
	}

	if hasRequest {
		return v.parseSigned(outer, requestJWT)
	}
	return v.parsePlaintext(outer)
}

// parsePlaintext 处理无 request 的明文授权请求，仅在双重策略均放行时可用。
func (v *Validator) parsePlaintext(outer map[string]string) (*AuthorizationRequest, error) {
	clientID, ok := outer["client_id"]
	if !ok || clientID == "" {
		return nil, wrapError("gate", ErrMissingClientID, "client_id is required")
	}
	if v.policy.RequireSignedRequestObject {
		return nil, wrapError("gate", ErrPlaintextDowngrade, "server policy requires a signed request object")
	}
	client, ok := v.store.Lookup(clientID)
	if !ok {
		return nil, wrapError("gate", ErrUnknownClient, "client %q is not registered", clientID)
	}
	if client.RequireSignedRequestObject() {
		return nil, wrapError("gate", ErrPlaintextDowngrade, "client %q is configured to require JAR", clientID)
	}

	params := make(map[string]string, len(outer))
	for key, value := range outer {
		if _, skip := transportParams[key]; skip {
			continue
		}
		params[key] = value
	}
	return resultFromParams(false, "", params), nil
}

// flattenValues 将 url.Values 收敛为单值映射，重复参数直接拒绝，
// 防止利用重复键制造解析差异。
func flattenValues(values url.Values) (map[string]string, error) {
	flat := make(map[string]string, len(values))
	for key, vals := range values {
		if len(vals) != 1 {
			return nil, wrapError("parse", ErrDuplicateParameter, "parameter %q appears %d times", key, len(vals))
		}
		flat[key] = vals[0]
	}
	return flat, nil
}

// resultFromParams 从可信参数映射构造结果，填充便捷字段。
func resultFromParams(signed bool, jti string, params map[string]string) *AuthorizationRequest {
	result := &AuthorizationRequest{
		Signed: signed,
		JTI:    jti,
		Params: params,
	}
	result.ClientID = params["client_id"]
	result.RedirectURI = params["redirect_uri"]
	result.Scope = params["scope"]
	result.State = params["state"]
	result.Nonce = params["nonce"]
	result.CodeChallenge = params["code_challenge"]
	return result
}

// parseSigned 执行 JWT 结构、密码学、声明、防混淆与防重放全套校验。
func (v *Validator) parseSigned(outer map[string]string, token string) (*AuthorizationRequest, error) {
	parts, err := parseCompactJWT(token)
	if err != nil {
		return nil, err
	}

	// 头部：alg 白名单（none/对称算法天然不在表内）。
	spec, err := v.algorithmSpec(parts.header.Alg)
	if err != nil {
		return nil, err
	}
	// 头部：typ 必须精确等于 oauth-authz-req+jwt，防令牌类型混淆。
	if parts.header.Typ != requestObjectTyp {
		return nil, wrapError("validate_header", ErrInvalidTyp, "got typ %q", parts.header.Typ)
	}
	// crit 扩展本实现不理解，按 RFC 7515 §4.1.11 必须拒绝。
	if parts.header.Crit != nil {
		return nil, wrapError("validate_header", ErrInvalidClaim, "crit header extensions are not supported")
	}

	// 载荷先解码（尚未可信），以取得 client_id 定位注册公钥。
	claims, err := unmarshalClaims(parts.rawClaimBytes)
	if err != nil {
		return nil, err
	}
	if claims.ClientID == "" {
		return nil, wrapError("validate_claim", ErrInvalidClaim, "request object must contain client_id")
	}
	if outerClientID, provided := outer["client_id"]; provided && outerClientID != claims.ClientID {
		return nil, wrapError("confusion", ErrOuterInnerConflict,
			"outer client_id %q does not match signed client_id %q", outerClientID, claims.ClientID)
	}

	client, ok := v.store.Lookup(claims.ClientID)
	if !ok {
		return nil, wrapError("validate_claim", ErrUnknownClient, "client %q is not registered", claims.ClientID)
	}

	key, ok := client.KeyByKID(parts.header.Kid)
	if !ok {
		if parts.header.Kid == "" {
			return nil, wrapError("verify_signature", ErrMissingKeyID, "kid header required to select a key")
		}
		return nil, wrapError("verify_signature", ErrKeyNotFound, "no key registered for kid %q", parts.header.Kid)
	}
	if err := verifySignature(spec, key, parts.signingInput, parts.signature); err != nil {
		return nil, err
	}

	// 验签通过后才进行声明级校验。
	if err := v.validateClaims(claims); err != nil {
		return nil, err
	}

	inner, err := innerParams(claims)
	if err != nil {
		return nil, err
	}
	if err := rejectOuterConfusion(outer, inner); err != nil {
		return nil, err
	}

	// 所有校验通过后原子占用 jti，重放在此被拦截。
	expiresAt := time.Unix(*claims.Expiry, 0)
	if !v.replay.Reserve(claims.ClientID, claims.JWTID, expiresAt) {
		return nil, wrapError("replay", ErrReplayedJTI, "jti %q reused by client %q", claims.JWTID, claims.ClientID)
	}

	return resultFromParams(true, claims.JWTID, inner), nil
}

// algorithmSpec 确认 alg 同时属于已知非对称算法表与策略白名单。
func (v *Validator) algorithmSpec(alg string) (algSpec, error) {
	spec, known := supportedAlgs[alg]
	if !known || !containsString(v.policy.AllowedAlgs, alg) {
		return algSpec{}, wrapError("validate_header", ErrAlgorithmNotAllowed,
			"alg %q rejected (only asymmetric algs are allowed)", alg)
	}
	return spec, nil
}

// validateClaims 执行 RFC 9101 §5 与 JWT 注册声明的强制校验。
func (v *Validator) validateClaims(claims *requestClaims) error {
	// 嵌套 request/request_uri 硬拒，杜绝递归/绕过。
	if _, nested := claims.RawParams["request"]; nested {
		return wrapError("validate_claim", ErrNestedRequest, "nested request claim is forbidden")
	}
	if _, nested := claims.RawParams["request_uri"]; nested {
		return wrapError("validate_claim", ErrNestedRequest, "nested request_uri claim is forbidden")
	}

	// iss 必须与 client_id 精确一致。
	if claims.Issuer == "" || claims.Issuer != claims.ClientID {
		return wrapError("validate_claim", ErrInvalidClaim,
			"iss %q must equal client_id %q", claims.Issuer, claims.ClientID)
	}

	// aud 必须精确且唯一地等于本授权服务器 Issuer。
	if len(claims.Audience) != 1 || claims.Audience[0] != v.policy.Issuer {
		return wrapError("validate_claim", ErrInvalidClaim,
			"aud %v must exactly match issuer %q", []string(claims.Audience), v.policy.Issuer)
	}

	// jti 必填且非空，供防重放使用。
	if claims.JWTID == "" {
		return wrapError("validate_claim", ErrInvalidClaim, "jti is required")
	}

	now := v.now()
	if claims.Expiry == nil {
		return wrapError("validate_claim", ErrInvalidClaim, "exp is required")
	}
	// exp 已过期（含 leeway）即拒。
	if now.After(time.Unix(*claims.Expiry, 0).Add(v.policy.Leeway)) {
		return wrapError("validate_claim", ErrInvalidClaim, "request object expired at %d", *claims.Expiry)
	}
	// nbf 若存在，不得处于未来（含 leeway）。
	if claims.NotBefore != nil && now.Add(v.policy.Leeway).Before(time.Unix(*claims.NotBefore, 0)) {
		return wrapError("validate_claim", ErrInvalidClaim, "request object not yet valid (nbf=%d)", *claims.NotBefore)
	}

	// 有效期上限：以 nbf 优先、iat 兜底作为生命周期起点。
	start := claims.NotBefore
	if start == nil {
		start = claims.IssuedAt
	}
	if start == nil {
		return wrapError("validate_claim", ErrInvalidClaim, "nbf or iat is required to bound the request object lifetime")
	}
	lifetimeSeconds := *claims.Expiry - *start
	if lifetimeSeconds < 0 {
		return wrapError("validate_claim", ErrInvalidClaim, "exp precedes nbf/iat")
	}
	if time.Duration(lifetimeSeconds)*time.Second > v.policy.MaxLifetime {
		return wrapError("validate_claim", ErrLifetimeExceeded,
			"lifetime %ds exceeds maximum %s", lifetimeSeconds, v.policy.MaxLifetime)
	}
	return nil
}

// innerParams 从已验签载荷提取字符串型业务参数；非字符串业务值直接拒绝。
func innerParams(claims *requestClaims) (map[string]string, error) {
	params := make(map[string]string, len(claims.RawParams))
	for key := range claims.RawParams {
		if _, registered := registeredClaimNames[key]; registered {
			continue
		}
		value, _, err := stringParam(claims.RawParams, key)
		if err != nil {
			return nil, err
		}
		params[key] = value
	}
	return params, nil
}

// registeredClaimNames 是 RFC 7519/JAR 的注册声明，不作为字符串业务参数提取。
// request/request_uri 虽被单独硬拒，也列入其中以保持边界清晰。
var registeredClaimNames = map[string]struct{}{
	"iss":         {},
	"sub":         {},
	"aud":         {},
	"exp":         {},
	"nbf":         {},
	"iat":         {},
	"jti":         {},
	"request":     {},
	"request_uri": {},
}

// rejectOuterConfusion 执行外层/内层防混淆：
// 除传输参数外，外层出现的任何键都必须在已验签内层存在且精确相等，
// 否则一律硬拒；有效业务参数永远只取自内层。
func rejectOuterConfusion(outer, inner map[string]string) error {
	for key, outerValue := range outer {
		if _, skip := transportParams[key]; skip {
			continue
		}
		innerValue, present := inner[key]
		if !present {
			return wrapError("confusion", ErrOuterInnerConflict,
				"outer unsigned parameter %q has no counterpart in the signed request object", key)
		}
		if innerValue != outerValue {
			return wrapError("confusion", ErrOuterInnerConflict,
				"outer %q=%q conflicts with signed value %q", key, outerValue, innerValue)
		}
	}
	// 显式复核题目点名的受保护参数，防止后续重构引入回归。
	for _, key := range protectedParams {
		if outerValue, hasOuter := outer[key]; hasOuter && inner[key] != outerValue {
			return wrapError("confusion", ErrOuterInnerConflict, "protected parameter %q was tampered", key)
		}
	}
	return nil
}
