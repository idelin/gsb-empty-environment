package jar

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"
)

const (
	testIssuer   = "https://as.example.gov.cn"
	testClientID = "gov-client-001"
	testKID      = "key-2026-11"
)

type testKeys struct {
	rsaPrivate *rsa.PrivateKey
	ecPrivate  *ecdsa.PrivateKey
	registry   *Registry
}

func setupKeys(t *testing.T, requireJAR bool) *testKeys {
	t.Helper()
	rsaPrivate, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	ecPrivate, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate EC key: %v", err)
	}
	registry := NewRegistry()
	client := NewRegisteredClient(testClientID, requireJAR)
	client.AddKey(testKID+"-rsa", &rsaPrivate.PublicKey)
	client.AddKey(testKID+"-ec", &ecPrivate.PublicKey)
	registry.Register(client)
	return &testKeys{rsaPrivate: rsaPrivate, ecPrivate: ecPrivate, registry: registry}
}

// signClaims 用指定私钥与 alg 构造紧凑 JWT，允许调用方覆盖头部与原始载荷，
// 以便构造 typ 混淆、none、对称算法等恶意样本。
func signClaims(t *testing.T, alg, typ string, claims map[string]any, key crypto.PrivateKey, kid string) string {
	t.Helper()
	header := map[string]any{"alg": alg, "typ": typ}
	if kid != "" {
		header["kid"] = kid
	}
	headerJSON := mustJSON(t, header)
	claimsJSON := mustJSON(t, claims)
	headerSeg := base64.RawURLEncoding.EncodeToString(headerJSON)
	payloadSeg := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signingInput := headerSeg + "." + payloadSeg

	var signature []byte
	switch {
	case alg == "none" || strings.HasPrefix(alg, "HS"):
		// 无签名场景：使用非空伪造签名保证结构合法，
		// 校验器必须在 alg 白名单阶段就拒绝，根本不会走到验签。
		signature = deterministicForgery(signingInput)
	case strings.HasPrefix(alg, "RS") || strings.HasPrefix(alg, "PS"):
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			// 跨密钥族样本：用任意私钥的确定性字节充当伪造 RSA 签名，
			// 验签端必须因密钥族不匹配/验签失败而拒绝。
			signature = deterministicForgery(signingInput)
			break
		}
		signature = signRSA(t, alg, rsaKey, []byte(signingInput))
	case strings.HasPrefix(alg, "ES"):
		ecKey, ok := key.(*ecdsa.PrivateKey)
		if !ok {
			signature = deterministicForgery(signingInput)
			break
		}
		signature = signEC(t, alg, ecKey, []byte(signingInput))
	default:
		t.Fatalf("unsupported test alg %s", alg)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}

// deterministicForgery 为跨密钥族负面用例生成一段确定性伪造签名。
func deterministicForgery(input string) []byte {
	sum := sha256.Sum256([]byte(input))
	return sum[:]
}

func signRSA(t *testing.T, alg string, priv *rsa.PrivateKey, input []byte) []byte {
	t.Helper()
	var (
		digest []byte
		hash   crypto.Hash
	)
	switch alg {
	case "RS256", "PS256":
		sum := sha256.Sum256(input)
		digest, hash = sum[:], crypto.SHA256
	case "RS384", "PS384":
		sum := sha512.Sum384(input)
		digest, hash = sum[:], crypto.SHA384
	case "RS512", "PS512":
		sum := sha512.Sum512(input)
		digest, hash = sum[:], crypto.SHA512
	default:
		t.Fatalf("unsupported RSA alg %s", alg)
	}
	if strings.HasPrefix(alg, "PS") {
		sig, err := rsa.SignPSS(rand.Reader, priv, hash, digest, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
		if err != nil {
			t.Fatalf("PSS sign: %v", err)
		}
		return sig
	}
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, hash, digest)
	if err != nil {
		t.Fatalf("PKCS1 sign: %v", err)
	}
	return sig
}

func signEC(t *testing.T, alg string, priv *ecdsa.PrivateKey, input []byte) []byte {
	t.Helper()
	var digest []byte
	switch alg {
	case "ES256":
		sum := sha256.Sum256(input)
		digest = sum[:]
	case "ES384":
		sum := sha512.Sum384(input)
		digest = sum[:]
	case "ES512":
		sum := sha512.Sum512(input)
		digest = sum[:]
	default:
		t.Fatalf("unsupported EC alg %s", alg)
	}
	sig, err := ecdsa.SignASN1(rand.Reader, priv, digest)
	if err != nil {
		t.Fatalf("ECDSA sign: %v", err)
	}
	return sig
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

// validClaims 构造一份满足全部强制规则的请求对象载荷。
func validClaims(now time.Time) map[string]any {
	jtiCounter++
	return map[string]any{
		"iss":            testClientID,
		"aud":            testIssuer,
		"client_id":      testClientID,
		"exp":            now.Add(120 * time.Second).Unix(),
		"nbf":            now.Unix(),
		"iat":            now.Unix(),
		"jti":            fmt.Sprintf("jti-%d-%d", now.UnixNano(), jtiCounter),
		"redirect_uri":   "https://client.example.gov.cn/callback",
		"scope":          "openid profile",
		"state":          "state-xyz",
		"nonce":          "nonce-abc",
		"code_challenge": "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM",
		"response_type":  "code",
	}
}

var jtiCounter int

func newTestValidator(store ClientStore) *Validator {
	return NewValidator(Policy{
		Issuer:                     testIssuer,
		RequireSignedRequestObject: true,
		MaxLifetime:                300 * time.Second,
		Leeway:                     30 * time.Second,
	}, store, NewMemoryReplayCache())
}

func queryWith(token string, extra ...string) url.Values {
	values := url.Values{}
	values.Set("request", token)
	for i := 0; i+1 < len(extra); i += 2 {
		values.Set(extra[i], extra[i+1])
	}
	return values
}

func assertSentinel(t *testing.T, err error, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("expected errors.Is(err, %v), got %v", target, err)
	}
}
