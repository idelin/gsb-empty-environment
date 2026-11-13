package netsec

import (
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
)

func encodeSegment(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func signRS256(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	input := encodeSegment(map[string]any{"alg": "RS256", "typ": "JWT"}) + "." + encodeSegment(claims)
	digest := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// 伪造令牌：alg 改成 HS256，用公钥字节当 HMAC 密钥（经典算法混淆攻击）。
func forgeHS256WithPublicKey(t *testing.T, pub *rsa.PublicKey, claims map[string]any) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	input := encodeSegment(map[string]any{"alg": "HS256", "typ": "JWT"}) + "." + encodeSegment(claims)
	mac := hmac.New(sha256.New, der)
	mac.Write([]byte(input))
	return input + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func signHS256(t *testing.T, secret []byte, claims map[string]any) string {
	t.Helper()
	input := encodeSegment(map[string]any{"alg": "HS256", "typ": "JWT"}) + "." + encodeSegment(claims)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(input))
	return input + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func TestSamePublicKeyRS256AcceptedHS256Rejected(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	claims := map[string]any{"sub": "alice", "scope": "read"}

	v, err := NewRSAVerifier(&key.PublicKey)
	if err != nil {
		t.Fatalf("NewRSAVerifier: %v", err)
	}

	got, err := v.Verify(signRS256(t, key, claims))
	if err != nil {
		t.Fatalf("valid RS256 token rejected: %v", err)
	}
	if got["sub"] != "alice" {
		t.Fatalf("unexpected claims: %v", got)
	}

	_, err = v.Verify(forgeHS256WithPublicKey(t, &key.PublicKey, claims))
	if !errors.Is(err, ErrAlgorithmMismatch) {
		t.Fatalf("forged HS256 token: got %v, want ErrAlgorithmMismatch", err)
	}
}

func TestHMACKeyNeverGoesThroughRSA(t *testing.T) {
	secret := []byte("resource-server-shared-secret")
	v, err := NewHMACVerifier(secret)
	if err != nil {
		t.Fatalf("NewHMACVerifier: %v", err)
	}
	if _, err := v.Verify(signHS256(t, secret, map[string]any{"sub": "bob"})); err != nil {
		t.Fatalf("valid HS256 token rejected: %v", err)
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	if _, err := v.Verify(signRS256(t, key, map[string]any{"sub": "bob"})); !errors.Is(err, ErrAlgorithmMismatch) {
		t.Fatalf("RS256 token on HMAC verifier: got %v, want ErrAlgorithmMismatch", err)
	}
}

func TestTamperedSignatureRejected(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	v, err := NewVerifier(&key.PublicKey)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	token := signRS256(t, key, map[string]any{"sub": "alice"})
	tampered := token[:len(token)-4] + "AAAA"
	if _, err := v.Verify(tampered); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("tampered token: got %v, want ErrInvalidSignature", err)
	}
}

func TestNewVerifierRejectsUnknownKeyType(t *testing.T) {
	if _, err := NewVerifier("not-a-key"); !errors.Is(err, ErrUnsupportedKeyType) {
		t.Fatalf("got %v, want ErrUnsupportedKeyType", err)
	}
}
