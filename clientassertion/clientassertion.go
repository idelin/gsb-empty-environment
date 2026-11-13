// Package clientassertion 为授权服务器实现 OAuth 2.0 private_key_jwt
// 客户端断言（RFC 7523 / OIDC Core 第 9 节）的服务端校验。
//
// 设计要点是消除受众注入与跨端点重放：aud 必须是单个字符串且逐字等于
// 本服务器的 issuer 标识，数组形式一律拒绝；jti 在 exp 之前按 client_id
// 分区只接受一次。签名本身由调用方通过 Verifier 回调验证，本包只负责
// 结构与声明校验以及防重放记账。
package clientassertion

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// AssertionTypeJWTBearer 是 private_key_jwt 唯一合法的断言类型。
const AssertionTypeJWTBearer = "urn:ietf:params:oauth:client-assertion-type:jwt-bearer"

const (
	// MaxLifetime 是 exp-iat 允许的最大秒数。
	MaxLifetime = 300 * time.Second
	// MaxFutureSkew 是 iat 相对当前时间允许的最大时钟偏移。
	MaxFutureSkew = 30 * time.Second
)

// Verifier 由调用方实现，用于验证断言签名并完成密钥解析（例如按
// client_id 找到客户端注册的 JWK 并校验 alg/公钥）。
//
// signingInput 是 JWT 的前两段（header.payload），signature 是第三段
// 的原始字节，header 已解析出来以便读取 kid/jwk 等参数。回调返回
// nil 即视为签名与算法可信；返回任何错误都会被包装为 ErrSignatureRejected。
type Verifier func(ctx context.Context, clientID string, header Header, signingInput, signature []byte) error

// Header 是断言的 JOSE 头部，只保留校验需要的字段，原始 JSON 仍可从
// Raw 读取自定义参数。
type Header struct {
	Alg string          `json:"alg"`
	Kid string          `json:"kid,omitempty"`
	Typ string          `json:"typ,omitempty"`
	Raw json.RawMessage `json:"-"`
}

// Request 是一次令牌端点请求中与客户端认证相关的参数。
type Request struct {
	// ClientID 是请求中的 client_id，必须与断言 iss/sub 一致。
	ClientID string
	// ClientSecret 是请求中的 client_secret；非空且同时携带断言时直接拒绝。
	ClientSecret string
	// Assertion 是 client_assertion 参数原文（compact JWT）。
	Assertion string
	// AssertionType 是 client_assertion_type 参数。
	AssertionType string
}

// RequestFromForm 从 application/x-www-form-urlencoded 表单值构造 Request。
// 入参可以是 http.Request 的 PostForm 或 Form。
func RequestFromForm(values map[string][]string) Request {
	first := func(key string) string {
		if vs := values[key]; len(vs) > 0 {
			return vs[0]
		}
		return ""
	}
	return Request{
		ClientID:      first("client_id"),
		ClientSecret:  first("client_secret"),
		Assertion:     first("client_assertion"),
		AssertionType: first("client_assertion_type"),
	}
}

// Store 负责 jti 防重放记账。实现必须对并发调用安全。
type Store interface {
	// CheckAndStore 在 clientID 分区内登记 jti。若该 jti 仍在 exp 之前的
	// 有效期内且已登记过，返回 ErrReplayedJTI；否则登记成功并返回 nil。
	// 已过期的同 jti 记录可以被新断言覆盖。
	CheckAndStore(ctx context.Context, clientID, jti string, exp time.Time) error
}

// Config 描述校验器的静态配置。
type Config struct {
	// Issuer 是本授权服务器的 issuer 标识，aud 必须逐字等于它，
	// 令牌端点地址等其他取值一律不接受。
	Issuer string
	// AllowedAlgs 是调用方的算法白名单，例如
	// []string{"RS256","ES256","PS256"}。none 与 HS* 永远拒绝；
	// 白名单里若混入这些值会在 New 时被剔除。
	AllowedAlgs []string
	// Verifier 是签名验证回调，必填。
	Verifier Verifier
	// Store 是 jti 防重放存储；为 nil 时使用 NewMemoryStore。
	Store Store
	// Now 返回当前时间；为 nil 时使用 time.Now，测试时可替换。
	Now func() time.Time
}

// Validator 对 private_key_jwt 断言执行完整校验，零值不可用，须经 New 构造。
type Validator struct {
	issuer      string
	allowedAlgs map[string]struct{}
	verifier    Verifier
	store       Store
	now         func() time.Time
}

// New 构造 Validator 并检查配置。
func New(cfg Config) (*Validator, error) {
	if strings.TrimSpace(cfg.Issuer) == "" {
		return nil, errors.New("clientassertion: Config.Issuer is required")
	}
	if cfg.Verifier == nil {
		return nil, errors.New("clientassertion: Config.Verifier is required")
	}
	allowed := make(map[string]struct{}, len(cfg.AllowedAlgs))
	for _, alg := range cfg.AllowedAlgs {
		if !algorithmAllowed(alg) {
			continue
		}
		allowed[alg] = struct{}{}
	}
	if len(allowed) == 0 {
		return nil, errors.New("clientassertion: Config.AllowedAlgs must contain at least one asymmetric algorithm")
	}
	store := cfg.Store
	if store == nil {
		store = NewMemoryStore()
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Validator{
		issuer:      cfg.Issuer,
		allowedAlgs: allowed,
		verifier:    cfg.Verifier,
		store:       store,
		now:         now,
	}, nil
}

// algorithmAllowed 集中表达硬编码的算法禁令：none 与 HMAC 对称系列
// 永远不得用于私钥断言，即使调用方误加入白名单。
func algorithmAllowed(alg string) bool {
	if alg == "" || strings.EqualFold(alg, "none") {
		return false
	}
	if strings.HasPrefix(strings.ToUpper(alg), "HS") {
		return false
	}
	return true
}

// Authenticate 校验一次请求中的 private_key_jwt 凭据。
// 成功返回经认证的 clientID；失败返回包装了包内哨兵错误的 error，
// 调用方可用 errors.Is 判断具体原因。
func (v *Validator) Authenticate(ctx context.Context, req Request) (string, error) {
	// 双重凭据必须先于一切解析被拒绝，避免歧义。
	if req.ClientSecret != "" && req.Assertion != "" {
		return "", ErrMultipleCredentials
	}
	if req.Assertion == "" {
		return "", ErrMissingAssertion
	}
	if req.AssertionType != AssertionTypeJWTBearer {
		return "", fmt.Errorf("%w: got %q", ErrUnsupportedAssertionType, req.AssertionType)
	}
	if req.ClientID == "" {
		return "", ErrMissingClientID
	}

	header, payload, signingInput, signature, err := v.parseCompact(req.Assertion)
	if err != nil {
		return "", err
	}
	if _, ok := v.allowedAlgs[header.Alg]; !ok {
		return "", fmt.Errorf("%w: alg %q", ErrDisallowedAlgorithm, header.Alg)
	}

	claims, err := parseClaims(payload)
	if err != nil {
		return "", err
	}
	if claims.Iss != req.ClientID {
		return "", fmt.Errorf("%w: iss %q", ErrInvalidIssuer, claims.Iss)
	}
	if claims.Sub != req.ClientID {
		return "", fmt.Errorf("%w: sub %q", ErrInvalidSubject, claims.Sub)
	}
	if err := validateAudience(claims.Aud, v.issuer); err != nil {
		return "", err
	}

	now := v.now()
	exp, err := claims.expiration()
	if err != nil {
		return "", ErrMissingExpiration
	}
	iat, err := claims.issuedAt()
	if err != nil {
		return "", ErrMissingIssuedAt
	}
	if !now.Before(exp) {
		return "", fmt.Errorf("%w: exp %s", ErrTokenExpired, exp.Format(time.RFC3339))
	}
	if lifetime := exp.Sub(iat); lifetime > MaxLifetime {
		return "", fmt.Errorf("%w: %s", ErrLifetimeTooLong, lifetime)
	}
	if skew := iat.Sub(now); skew > MaxFutureSkew {
		return "", fmt.Errorf("%w: iat %s", ErrIssuedAtInFuture, iat.Format(time.RFC3339))
	}
	if claims.JTI == "" {
		return "", ErrMissingJTI
	}

	if err := v.verifier(ctx, req.ClientID, header, signingInput, signature); err != nil {
		return "", fmt.Errorf("%w: %v", ErrSignatureRejected, err)
	}

	// 防重放放在最后、且由 Store 保证原子性，避免未通过校验的断言污染记账。
	if err := v.store.CheckAndStore(ctx, req.ClientID, claims.JTI, exp); err != nil {
		return "", err
	}
	return req.ClientID, nil
}

func (v *Validator) parseCompact(token string) (Header, []byte, []byte, []byte, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return Header{}, nil, nil, nil, ErrMalformedAssertion
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return Header{}, nil, nil, nil, fmt.Errorf("%w: header encoding: %v", ErrMalformedAssertion, err)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Header{}, nil, nil, nil, fmt.Errorf("%w: payload encoding: %v", ErrMalformedAssertion, err)
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return Header{}, nil, nil, nil, fmt.Errorf("%w: signature encoding: %v", ErrMalformedAssertion, err)
	}
	var header Header
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return Header{}, nil, nil, nil, fmt.Errorf("%w: header json: %v", ErrMalformedAssertion, err)
	}
	header.Raw = append(header.Raw[:0], headerBytes...)
	return header, payload, []byte(parts[0] + "." + parts[1]), signature, nil
}

type rawClaims struct {
	Iss string          `json:"iss"`
	Sub string          `json:"sub"`
	Aud json.RawMessage `json:"aud"`
	Exp json.RawMessage `json:"exp"`
	Iat json.RawMessage `json:"iat"`
	JTI string          `json:"jti"`
}

func parseClaims(payload []byte) (rawClaims, error) {
	var claims rawClaims
	dec := json.NewDecoder(strings.NewReader(string(payload)))
	dec.UseNumber()
	if err := dec.Decode(&claims); err != nil {
		return rawClaims{}, fmt.Errorf("%w: payload json: %v", ErrMalformedAssertion, err)
	}
	return claims, nil
}

// validateAudience 强制 aud 为单个 JSON 字符串且逐字等于 issuer。
// 任何数组形式（即使包含正确值）、null 或其他类型一律拒绝，
// 从根源上杜绝跨端点受众注入。
func validateAudience(raw json.RawMessage, issuer string) error {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" || trimmed[0] == '[' {
		return fmt.Errorf("%w: aud must be a single string, got %s", ErrInvalidAudience, trimmed)
	}
	var aud string
	if err := json.Unmarshal(raw, &aud); err != nil {
		return fmt.Errorf("%w: aud must be a string: %v", ErrInvalidAudience, err)
	}
	if aud != issuer {
		return fmt.Errorf("%w: %q", ErrInvalidAudience, aud)
	}
	return nil
}

func numericDate(raw json.RawMessage) (time.Time, error) {
	var number json.Number
	if err := json.Unmarshal(raw, &number); err != nil {
		return time.Time{}, err
	}
	seconds, err := number.Int64()
	if err != nil {
		// 兼容 1700000000.5 形式的分数秒。
		frac, ferr := number.Float64()
		if ferr != nil {
			return time.Time{}, ferr
		}
		return time.UnixMicro(int64(frac * 1e6)).UTC(), nil
	}
	return time.Unix(seconds, 0).UTC(), nil
}

func (c rawClaims) expiration() (time.Time, error) {
	if len(c.Exp) == 0 {
		return time.Time{}, errors.New("missing exp")
	}
	return numericDate(c.Exp)
}

func (c rawClaims) issuedAt() (time.Time, error) {
	if len(c.Iat) == 0 {
		return time.Time{}, errors.New("missing iat")
	}
	return numericDate(c.Iat)
}
