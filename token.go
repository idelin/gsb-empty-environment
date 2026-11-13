package netsec

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Algorithm 标识凭证使用的签名算法。目前仅支持 EdDSA（Ed25519）。
const Algorithm = "EdDSA"

// rawHeader / rawPayload / rawEnvelope 是凭证的线上表示，采用 JWT 风格的
// base64url(header).base64url(payload).base64url(signature) 三段式编码。
type rawHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

type rawPayload struct {
	Issuer   string       `json:"iss"`
	Audience string       `json:"aud"`
	Expires  int64        `json:"exp"`
	Caps     []Capability `json:"caps"`
	Prf      string       `json:"prf,omitempty"`
}

type rawEnvelope struct {
	header    rawHeader
	payload   rawPayload
	signature []byte
	encoding  string
}

// Capability 是一条原子授权：对 Resource 执行 Action。
//
// Resource 与 Action 均支持末尾通配符 "*"，例如 "fs:/home/alice/*"
// 与 "crud/*"。不带 "*" 时按精确字符串匹配。
type Capability struct {
	Resource string `json:"resource"`
	Action   string `json:"action"`
}

// Token 是已解析的委托凭证（UCAN）。
type Token struct {
	raw rawEnvelope
}

// Issuer 返回凭证签发者的 DID（did:key:...）。
func (t *Token) Issuer() string { return t.raw.payload.Issuer }

// Audience 返回凭证受众（被委托方）的 DID。
func (t *Token) Audience() string { return t.raw.payload.Audience }

// Expires 返回凭证的过期时刻（UTC）。
func (t *Token) Expires() time.Time { return time.Unix(t.raw.payload.Expires, 0).UTC() }

// Capabilities 返回凭证携带的能力集合的副本。
func (t *Token) Capabilities() []Capability {
	caps := make([]Capability, len(t.raw.payload.Caps))
	copy(caps, t.raw.payload.Caps)
	return caps
}

// Proof 返回上一跳凭证的引用（编码后的父凭证）；根凭证返回空串。
func (t *Token) Proof() string { return t.raw.payload.Prf }

// Encode 返回凭证的紧凑三段式字符串表示，既可在网络上传输，也可作为
// 下一跳凭证的 prf 引用。
func (t *Token) Encode() string { return t.raw.encoding }

// Sign 使用签发者的 Ed25519 私钥构造并签名一张委托凭证。
//
// issuerKey 与 audienceKey 为 Ed25519 公钥，会被编码成 did:key 形式写入
// iss/aud 字段。expires 为凭证的绝对过期时刻。proof 为父凭证的紧凑编码，
// 根凭证传空串。
func Sign(issuerKey ed25519.PrivateKey, audienceKey ed25519.PublicKey,
	expires time.Time, caps []Capability, proof string) (*Token, error) {

	if len(issuerKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%w: issuer key is not an Ed25519 private key", ErrMalformedToken)
	}
	if len(audienceKey) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%w: audience key is not an Ed25519 public key", ErrMalformedToken)
	}
	if expires.IsZero() {
		return nil, fmt.Errorf("%w: expiry time is required", ErrMalformedToken)
	}

	header := rawHeader{Alg: Algorithm, Typ: "JWT"}
	payload := rawPayload{
		Issuer:   DIDFromPublicKey(issuerKey.Public().(ed25519.PublicKey)),
		Audience: DIDFromPublicKey(audienceKey),
		Expires:  expires.UTC().Unix(),
		Caps:     append([]Capability(nil), caps...),
		Prf:      proof,
	}

	headerBytes, err := json.Marshal(header)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedToken, err)
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedToken, err)
	}

	enc := base64.RawURLEncoding
	head := enc.EncodeToString(headerBytes)
	body := enc.EncodeToString(payloadBytes)
	signingInput := head + "." + body
	signature := ed25519.Sign(issuerKey, []byte(signingInput))

	env := rawEnvelope{
		header:    header,
		payload:   payload,
		signature: signature,
		encoding:  signingInput + "." + enc.EncodeToString(signature),
	}
	return &Token{raw: env}, nil
}

// Parse 解码并校验一张凭证的结构与签名，但不做委托链语义校验。
func Parse(encoded string) (*Token, error) {
	parts := strings.Split(encoded, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("%w: token must have three parts", ErrMalformedToken)
	}

	enc := base64.RawURLEncoding
	headerBytes, err := enc.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("%w: bad header encoding: %v", ErrMalformedToken, err)
	}
	payloadBytes, err := enc.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("%w: bad payload encoding: %v", ErrMalformedToken, err)
	}
	signature, err := enc.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("%w: bad signature encoding: %v", ErrMalformedToken, err)
	}

	var header rawHeader
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return nil, fmt.Errorf("%w: bad header: %v", ErrMalformedToken, err)
	}
	if header.Alg != Algorithm {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedAlgorithm, header.Alg)
	}

	var payload rawPayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return nil, fmt.Errorf("%w: bad payload: %v", ErrMalformedToken, err)
	}
	if payload.Issuer == "" || payload.Audience == "" || payload.Expires == 0 {
		return nil, fmt.Errorf("%w: iss, aud and exp are required", ErrMalformedToken)
	}
	if len(payload.Caps) == 0 {
		return nil, fmt.Errorf("%w: at least one capability is required", ErrMalformedToken)
	}

	issuerKey, err := PublicKeyFromDID(payload.Issuer)
	if err != nil {
		return nil, err
	}
	if !ed25519.Verify(issuerKey, []byte(parts[0]+"."+parts[1]), signature) {
		return nil, ErrInvalidSignature
	}

	env := rawEnvelope{
		header:    header,
		payload:   payload,
		signature: signature,
		encoding:  encoded,
	}
	return &Token{raw: env}, nil
}
