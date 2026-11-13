package netsec

import (
	"crypto"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// 算法族：验证算法由密钥的真实类型决定，而不是令牌头里的 alg 字段。
const (
	AlgRS256 = "RS256"
	AlgHS256 = "HS256"
)

var (
	ErrMalformedToken     = errors.New("netsec: malformed token")
	ErrAlgorithmMismatch  = errors.New("netsec: token alg does not match the key's algorithm family")
	ErrInvalidSignature   = errors.New("netsec: invalid signature")
	ErrUnsupportedKeyType = errors.New("netsec: unsupported key type")
)

// Verifier 按密钥类型绑死算法族，杜绝 RS256/HS256 混淆：
// RSA 公钥只接受 RS256，HMAC 密钥只接受 HS256。
type Verifier struct {
	alg string
	rsa *rsa.PublicKey
	mac []byte
}

// NewRSAVerifier 构造只接受 RS256 的验证器。公钥字节永远不会被当作 HMAC 密钥。
func NewRSAVerifier(pub *rsa.PublicKey) (*Verifier, error) {
	if pub == nil {
		return nil, fmt.Errorf("%w: nil *rsa.PublicKey", ErrUnsupportedKeyType)
	}
	return &Verifier{alg: AlgRS256, rsa: pub}, nil
}

// NewHMACVerifier 构造只接受 HS256 的验证器。HMAC 密钥永远不会走 RSA 验证。
func NewHMACVerifier(secret []byte) (*Verifier, error) {
	if len(secret) == 0 {
		return nil, fmt.Errorf("%w: empty HMAC secret", ErrUnsupportedKeyType)
	}
	key := make([]byte, len(secret))
	copy(key, secret)
	return &Verifier{alg: AlgHS256, mac: key}, nil
}

// NewVerifier 按密钥的真实类型选择算法族。
func NewVerifier(key any) (*Verifier, error) {
	switch k := key.(type) {
	case *rsa.PublicKey:
		return NewRSAVerifier(k)
	case []byte:
		return NewHMACVerifier(k)
	default:
		return nil, fmt.Errorf("%w: unsupported key type %T", ErrUnsupportedKeyType, key)
	}
}

// Algorithm 返回该验证器唯一接受的 alg。
func (v *Verifier) Algorithm() string { return v.alg }

// Verify 校验令牌签名并返回 claims。alg 与密钥算法族不一致时直接拒绝。
func (v *Verifier) Verify(token string) (map[string]any, error) {
	header, claims, signingInput, signature, err := parse(token)
	if err != nil {
		return nil, err
	}
	if header.Alg != v.alg {
		return nil, fmt.Errorf("%w: token alg %q, verifier requires %q", ErrAlgorithmMismatch, header.Alg, v.alg)
	}
	switch v.alg {
	case AlgRS256:
		digest := sha256.Sum256([]byte(signingInput))
		if err := rsa.VerifyPKCS1v15(v.rsa, crypto.SHA256, digest[:], signature); err != nil {
			return nil, ErrInvalidSignature
		}
	case AlgHS256:
		mac := hmac.New(sha256.New, v.mac)
		mac.Write([]byte(signingInput))
		if !hmac.Equal(mac.Sum(nil), signature) {
			return nil, ErrInvalidSignature
		}
	}
	return claims, nil
}

type tokenHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

func parse(token string) (tokenHeader, map[string]any, string, []byte, error) {
	var header tokenHeader
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return header, nil, "", nil, ErrMalformedToken
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return header, nil, "", nil, fmt.Errorf("%w: bad header encoding", ErrMalformedToken)
	}
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return header, nil, "", nil, fmt.Errorf("%w: bad header json", ErrMalformedToken)
	}
	claimsBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return header, nil, "", nil, fmt.Errorf("%w: bad claims encoding", ErrMalformedToken)
	}
	var claims map[string]any
	if err := json.Unmarshal(claimsBytes, &claims); err != nil {
		return header, nil, "", nil, fmt.Errorf("%w: bad claims json", ErrMalformedToken)
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return header, nil, "", nil, fmt.Errorf("%w: bad signature encoding", ErrMalformedToken)
	}
	return header, claims, parts[0] + "." + parts[1], signature, nil
}
