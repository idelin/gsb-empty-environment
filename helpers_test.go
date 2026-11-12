package netsec

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
)

// fakeResolver 是静态 DNS 解析器，使所有测试无需触达真实 DNS。
type fakeResolver struct {
	hosts map[string][]net.IPAddr
	err   error
}

func (r fakeResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	if r.err != nil {
		return nil, r.err
	}
	addrs, ok := r.hosts[strings.TrimSuffix(host, ".")]
	if !ok {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	return addrs, nil
}

// testGuard 构建一个允许回环地址（用于 httptest）、其余仍按公网策略判定的 Guard。
func testGuard(t *testing.T, resolver Resolver, patterns ...string) *Guard {
	t.Helper()
	parsed := make([]HostPattern, 0, len(patterns))
	for _, pattern := range patterns {
		parsed = append(parsed, mustPattern(t, pattern))
	}
	return &Guard{
		Whitelist: parsed,
		Resolver:  resolver,
		IPAllowed: allowLoopbackOnly,
	}
}

func mustPattern(t *testing.T, pattern string) HostPattern {
	t.Helper()
	parsed, err := ParseHostPattern(pattern)
	if err != nil {
		t.Fatalf("ParseHostPattern(%q): %v", pattern, err)
	}
	return parsed
}

func allowLoopbackOnly(ip net.IP) bool {
	if ip.IsLoopback() {
		return true
	}
	return IsPublicIP(ip)
}

// generateSubscriptionKeys 生成模拟用户代理的 P-256 公钥与 16 字节 auth。
func generateSubscriptionKeys(t *testing.T) SubscriptionKeys {
	t.Helper()
	ephemeral, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate UA key: %v", err)
	}
	auth := make([]byte, authSecretLen)
	if _, err := rand.Read(auth); err != nil {
		t.Fatalf("generate auth: %v", err)
	}
	return SubscriptionKeys{P256DH: ephemeral.PublicKey().Bytes(), Auth: auth}
}

// uaPrivateKey 生成 ecdsa 私钥并返回，供解密回验使用。
func uaPrivateKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate ECDSA key: %v", err)
	}
	return key
}

// decryptAES128GCM 在测试侧按 RFC 8291 解密消息体，验证加密实现可被用户代理还原。
func decryptAES128GCM(t *testing.T, uaPrivate *ecdh.PrivateKey, authSecret, message []byte) []byte {
	t.Helper()
	if len(message) < saltLen+4+1 {
		t.Fatal("message too short for aes128gcm header")
	}
	salt := message[:saltLen]
	recordSizeValue := binary.BigEndian.Uint32(message[saltLen : saltLen+4])
	idLen := int(message[saltLen+4])
	headerLen := saltLen + 4 + 1 + idLen
	asPublicRaw := message[saltLen+5 : headerLen]
	ciphertext := message[headerLen:]

	asPublic, err := uaPrivate.Curve().NewPublicKey(asPublicRaw)
	if err != nil {
		t.Fatalf("parse sender public key: %v", err)
	}
	ecdhSecret, err := uaPrivate.ECDH(asPublic)
	if err != nil {
		t.Fatalf("ECDH: %v", err)
	}

	uaPublicRaw := uaPrivate.PublicKey().Bytes()
	authPRK := hkdfExtract(authSecret, ecdhSecret)
	intermediateIKM := hkdfExpand(authPRK, buildKeyInfo(uaPublicRaw, asPublicRaw), sha256.Size)
	recordPRK := hkdfExtract(salt, intermediateIKM)
	cek := hkdfExpand(recordPRK, contentEncodingInfo("aes128gcm", cekLen), cekLen)
	nonce := hkdfExpand(recordPRK, contentEncodingInfo("nonce", nonceLen), nonceLen)

	block, err := aes.NewCipher(cek)
	if err != nil {
		t.Fatalf("new cipher: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("new GCM: %v", err)
	}
	record, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if recordSizeValue != recordSize {
		t.Fatalf("record size = %d, want %d", recordSizeValue, recordSize)
	}
	if len(record) == 0 || record[len(record)-1] != 0x02 {
		t.Fatal("missing record padding delimiter")
	}
	return record[:len(record)-1]
}

func readBody(t *testing.T, body io.ReadCloser) string {
	t.Helper()
	data, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(data)
}

func assertErrorIs(t *testing.T, err error, target error) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error %v, got nil", target)
	}
	if !errors.Is(err, target) {
		t.Fatalf("error = %v, want wrap of %v", err, target)
	}
}
