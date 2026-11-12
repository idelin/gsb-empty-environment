package netsec

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"strings"
	"testing"
	"time"
)

func TestVAPIDKeyRoundTripAndFingerprint(t *testing.T) {
	credentials, err := NewVAPIDCredentials("mailto:push@example.com")
	if err != nil {
		t.Fatalf("new credentials: %v", err)
	}
	raw, err := EncodeVAPIDPublicKey(&credentials.PrivateKey.PublicKey)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if len(raw) != 65 || raw[0] != 0x04 {
		t.Fatalf("unexpected point encoding length %d", len(raw))
	}

	parsed, err := ParseVAPIDPublicKey(raw)
	if err != nil {
		t.Fatalf("parse raw point: %v", err)
	}
	if !parsed.Equal(&credentials.PrivateKey.PublicKey) {
		t.Fatal("parsed key differs from original")
	}

	// base64url 文本形式同样可解析。
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	fromText, err := DecodeVAPIDPublicKey(encoded)
	if err != nil {
		t.Fatalf("decode base64url: %v", err)
	}
	if !fromText.Equal(&credentials.PrivateKey.PublicKey) {
		t.Fatal("base64url parsed key differs")
	}

	// SPKI DER 形式也应兼容。
	der, err := marshalSPKI(&credentials.PrivateKey.PublicKey)
	if err != nil {
		t.Fatalf("spki: %v", err)
	}
	fromDER, err := ParseVAPIDPublicKey(der)
	if err != nil {
		t.Fatalf("parse spki: %v", err)
	}
	if !fromDER.Equal(&credentials.PrivateKey.PublicKey) {
		t.Fatal("SPKI parsed key differs")
	}

	fp1, _ := VAPIDFingerprint(parsed)
	fp2, _ := VAPIDFingerprint(fromDER)
	if fp1 != fp2 {
		t.Fatal("fingerprints of equivalent encodings must match")
	}
}

func TestParseVAPIDPublicKeyRejectsBadInput(t *testing.T) {
	if _, err := ParseVAPIDPublicKey(nil); err == nil {
		t.Fatal("empty key should fail")
	}
	if _, err := ParseVAPIDPublicKey(make([]byte, 65)); err == nil {
		t.Fatal("all-zero point should fail")
	}
	if _, err := DecodeVAPIDPublicKey("!!!not-base64!!!"); err == nil {
		t.Fatal("invalid base64 should fail")
	}

	// 非 P-256 曲线的公钥应被拒绝。
	otherCurve, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatalf("generate P-384 key: %v", err)
	}
	if _, err := VAPIDFingerprint(&otherCurve.PublicKey); err == nil {
		t.Fatal("P-384 key must not produce a VAPID fingerprint")
	}
}

func TestVAPIDAuthorizationJWT(t *testing.T) {
	fixed := time.Date(2026, 11, 12, 8, 0, 0, 0, time.UTC)
	credentials, err := NewVAPIDCredentials("mailto:admin@example.com")
	if err != nil {
		t.Fatalf("credentials: %v", err)
	}
	credentials.now = func() time.Time { return fixed }

	headerValue, err := credentials.Authorization("https://push.example.com")
	if err != nil {
		t.Fatalf("authorization: %v", err)
	}
	if !strings.HasPrefix(headerValue, "vapid-t=") || !strings.Contains(headerValue, ",k=") {
		t.Fatalf("unexpected Authorization format: %s", headerValue)
	}

	parts := strings.SplitN(strings.TrimPrefix(headerValue, "vapid-t="), ",k=", 2)
	token, encodedKey := parts[0], parts[1]
	segments := strings.Split(token, ".")
	if len(segments) != 3 {
		t.Fatalf("JWT must have 3 segments, got %d", len(segments))
	}

	headerJSON, _ := base64.RawURLEncoding.DecodeString(segments[0])
	claimsJSON, _ := base64.RawURLEncoding.DecodeString(segments[1])
	signature, _ := base64.RawURLEncoding.DecodeString(segments[2])

	var header map[string]string
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		t.Fatalf("header json: %v", err)
	}
	if header["alg"] != "ES256" || header["typ"] != "JWT" {
		t.Fatalf("bad JWT header: %v", header)
	}

	var claims map[string]any
	if err := json.Unmarshal(claimsJSON, &claims); err != nil {
		t.Fatalf("claims json: %v", err)
	}
	if claims["aud"] != "https://push.example.com" || claims["sub"] != "mailto:admin@example.com" {
		t.Fatalf("bad claims: %v", claims)
	}
	if int64(claims["exp"].(float64)) != fixed.Add(vapidTokenTTL).Unix() {
		t.Fatal("exp claim mismatch")
	}
	if int64(claims["iat"].(float64)) != fixed.Unix() {
		t.Fatal("iat claim mismatch")
	}

	// 用公钥验证 ES256 签名（R||S 定长拼接）。
	if len(signature) != 64 {
		t.Fatalf("signature length = %d, want 64", len(signature))
	}
	r := new(big.Int).SetBytes(signature[:32])
	s := new(big.Int).SetBytes(signature[32:])
	digest := sha256.Sum256([]byte(segments[0] + "." + segments[1]))
	if !ecdsa.Verify(&credentials.PrivateKey.PublicKey, digest[:], r, s) {
		t.Fatal("JWT signature verification failed")
	}

	// k= 中的公钥应与凭据公钥一致。
	keyBytes, err := base64.RawURLEncoding.DecodeString(encodedKey)
	if err != nil {
		t.Fatalf("decode k: %v", err)
	}
	parsedKey, err := ParseVAPIDPublicKey(keyBytes)
	if err != nil {
		t.Fatalf("parse k: %v", err)
	}
	if !parsedKey.Equal(&credentials.PrivateKey.PublicKey) {
		t.Fatal("Authorization public key mismatch")
	}
}
