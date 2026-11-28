package jar

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"hash"
)

// 授权请求对象类型头（RFC 9101 §5.1）。
const requestObjectTyp = "oauth-authz-req+jwt"

// algSpec 描述一个允许的非对称签名算法。
// 仅登记依赖 crypto/ecdsa、crypto/rsa、crypto/sha256（以及标准库 sha512）
// 即可完成验签的算法；none 与 HS* 等对称算法永远不在表内。
type algSpec struct {
	hash    crypto.Hash
	pss     bool // RS* 使用 PKCS#1 v1.5，PS* 使用 PSS
	ec      bool // ES* 使用 ECDSA
	rsa     bool // RS*/PS* 使用 RSA
	newHash func() hash.Hash
}

// supportedAlgs 是默认白名单。宿主可通过 Policy.AllowedAlgs 收窄。
// 全部为非对称算法：none、HS256/HS384/HS512 一律无法通过校验。
var supportedAlgs = map[string]algSpec{
	"RS256": {hash: crypto.SHA256, rsa: true, newHash: sha256.New},
	"RS384": {hash: crypto.SHA384, rsa: true, newHash: sha512.New384},
	"RS512": {hash: crypto.SHA512, rsa: true, newHash: sha512.New},
	"PS256": {hash: crypto.SHA256, rsa: true, pss: true, newHash: sha256.New},
	"PS384": {hash: crypto.SHA384, rsa: true, pss: true, newHash: sha512.New384},
	"PS512": {hash: crypto.SHA512, rsa: true, pss: true, newHash: sha512.New},
	"ES256": {hash: crypto.SHA256, ec: true, newHash: sha256.New},
	"ES384": {hash: crypto.SHA384, ec: true, newHash: sha512.New384},
	"ES512": {hash: crypto.SHA512, ec: true, newHash: sha512.New},
}

// DefaultAllowedAlgs 是策略未显式配置时使用的非对称白名单。
var DefaultAllowedAlgs = []string{"RS256", "PS256", "ES256"}

type jwtHeader struct {
	Alg  string `json:"alg"`
	Typ  string `json:"typ"`
	Kid  string `json:"kid"`
	Crit any    `json:"crit,omitempty"`
}

type jwtParts struct {
	header        jwtHeader
	rawHeader     []byte
	rawPayload    []byte
	signingInput  []byte // rawHeader + "." + rawPayload
	signature     []byte
	rawClaimBytes []byte
}

func base64RawDecode(segment string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(segment)
}

// parseCompactJWT 拆分并解码紧凑 JWT，不做任何信任判断。
func parseCompactJWT(token string) (*jwtParts, error) {
	// 先拒绝 base64 填充与多余分段，防止解析器差异。
	firstDot := indexByte(token, '.')
	if firstDot < 0 {
		return nil, wrapError("parse", ErrMalformedJWT, "JWT must contain three dot-separated segments")
	}
	rest := token[firstDot+1:]
	secondDot := indexByte(rest, '.')
	if secondDot < 0 {
		return nil, wrapError("parse", ErrMalformedJWT, "JWT must contain three dot-separated segments")
	}
	if indexByte(rest[secondDot+1:], '.') >= 0 {
		return nil, wrapError("parse", ErrMalformedJWT, "JWT must contain exactly three segments")
	}
	headerSeg := token[:firstDot]
	payloadSeg := rest[:secondDot]
	sigSeg := rest[secondDot+1:]
	if headerSeg == "" || payloadSeg == "" || sigSeg == "" {
		return nil, wrapError("parse", ErrMalformedJWT, "empty JWT segment")
	}
	rawHeader, err := base64RawDecode(headerSeg)
	if err != nil {
		return nil, wrapError("parse", ErrMalformedJWT, "header is not base64url: %v", err)
	}
	rawPayload, err := base64RawDecode(payloadSeg)
	if err != nil {
		return nil, wrapError("parse", ErrMalformedJWT, "payload is not base64url: %v", err)
	}
	signature, err := base64RawDecode(sigSeg)
	if err != nil {
		return nil, wrapError("parse", ErrMalformedJWT, "signature is not base64url: %v", err)
	}
	parts := &jwtParts{
		rawHeader:     rawHeader,
		rawPayload:    rawPayload,
		signingInput:  []byte(headerSeg + "." + payloadSeg),
		signature:     signature,
		rawClaimBytes: rawPayload,
	}
	if err := unmarshalJSON(rawHeader, &parts.header); err != nil {
		return nil, wrapError("parse", ErrMalformedJWT, "header is not valid JSON: %v", err)
	}
	return parts, nil
}

// verifySignature 按 alg 规范执行非对称验签。
// 密钥类型必须与算法族匹配，否则视为验签失败而非静默放行。
func verifySignature(spec algSpec, key crypto.PublicKey, signingInput, signature []byte) error {
	digester := spec.newHash()
	if _, err := digester.Write(signingInput); err != nil {
		return wrapError("verify_signature", ErrSignatureInvalid, "hashing failed: %v", err)
	}
	digest := digester.Sum(nil)
	switch {
	case spec.rsa:
		rsaKey, ok := key.(*rsa.PublicKey)
		if !ok {
			return wrapError("verify_signature", ErrSignatureInvalid, "alg requires an RSA public key")
		}
		if spec.pss {
			if err := rsa.VerifyPSS(rsaKey, spec.hash, digest, signature, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash}); err != nil {
				return wrapError("verify_signature", ErrSignatureInvalid, "PSS verification failed: %v", err)
			}
			return nil
		}
		if err := rsa.VerifyPKCS1v15(rsaKey, spec.hash, digest, signature); err != nil {
			return wrapError("verify_signature", ErrSignatureInvalid, "PKCS#1 v1.5 verification failed: %v", err)
		}
		return nil
	case spec.ec:
		ecKey, ok := key.(*ecdsa.PublicKey)
		if !ok {
			return wrapError("verify_signature", ErrSignatureInvalid, "alg requires an ECDSA public key")
		}
		if !ecdsa.VerifyASN1(ecKey, digest, signature) {
			return wrapError("verify_signature", ErrSignatureInvalid, "ECDSA verification failed")
		}
		return nil
	default:
		return wrapError("verify_signature", ErrAlgorithmNotAllowed, "unsupported algorithm family")
	}
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}
