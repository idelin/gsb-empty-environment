package jar

import "encoding/json"

func unmarshalJSON(data []byte, v any) error { return json.Unmarshal(data, v) }

// audience 是 RFC 7519 的 aud 声明：序列化形态可能是字符串或字符串数组。
type audience []string

// UnmarshalJSON 强制 aud 只能是 JSON 字符串或字符串数组，拒绝数字/对象等歧义形态。
func (a *audience) UnmarshalJSON(raw []byte) error {
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		*a = audience{single}
		return nil
	}
	var multi []string
	if err := json.Unmarshal(raw, &multi); err != nil {
		return err
	}
	*a = audience(multi)
	return nil
}

// requestClaims 是 JAR 请求对象中与安全相关的注册声明。
// 其余业务参数通过 RawParams 以原始键名读取。
type requestClaims struct {
	Issuer    string   `json:"iss"`
	Audience  audience `json:"aud"`
	Expiry    *int64   `json:"exp"`
	NotBefore *int64   `json:"nbf"`
	IssuedAt  *int64   `json:"iat"`
	JWTID     string   `json:"jti"`
	ClientID  string   `json:"client_id"`

	// RawParams 保留全部载荷键的原始 JSON，用于：
	// 1) 检测嵌套 request/request_uri；
	// 2) 提取 redirect_uri/scope/state/nonce/code_challenge 等业务参数。
	RawParams map[string]json.RawMessage `json:"-"`
}

// unmarshalClaims 先解码到 map 以保留原始键名，再严格解析注册声明。
func unmarshalClaims(raw []byte) (*requestClaims, error) {
	var rawParams map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawParams); err != nil {
		return nil, wrapError("parse", ErrMalformedJWT, "payload is not valid JSON: %v", err)
	}
	var claims requestClaims
	if err := json.Unmarshal(raw, &claims); err != nil {
		return nil, wrapError("parse", ErrMalformedJWT, "invalid claim type: %v", err)
	}
	claims.RawParams = rawParams
	return &claims, nil
}

// stringParam 从原始 JSON 中读取字符串声明；不存在返回 ("", false, nil)，
// 类型不是 JSON 字符串时返回错误，杜绝数字/数组被隐式字符串化。
func stringParam(raw map[string]json.RawMessage, key string) (string, bool, error) {
	value, ok := raw[key]
	if !ok {
		return "", false, nil
	}
	var s string
	if err := json.Unmarshal(value, &s); err != nil {
		return "", true, wrapError("parse_claim", ErrInvalidClaim, "claim %q must be a JSON string", key)
	}
	return s, true, nil
}
