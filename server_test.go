package netsec

import (
	"bytes"
	"crypto/rand"
	"sync"
	"testing"
	"time"
)

// fakeClock 是可手动推进的单调时钟，供 TTL 缓存测试使用。
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// newTestServer 构造使用假时钟的校验服务，默认 AES-128-GCM。
func newTestServer(t *testing.T, clock *fakeClock) *Server {
	t.Helper()
	key := bytes.Repeat([]byte{0xAB}, 16)
	srv, err := NewServerWithConfig(ServerConfig{
		AEAD:          AEADAESGCM128,
		CookieKey:     key,
		ReplayTTL:     time.Minute,
		CacheCapacity: 8,
		Now:           clock.now,
	})
	if err != nil {
		t.Fatalf("NewServerWithConfig: %v", err)
	}
	return srv
}

// sealClientRequest 模拟 NTS 客户端：用 C2S 密钥封签请求，
// 返回完整 NTP 报文与本次使用的 UID/Cookie 原始值。
func sealClientRequest(t *testing.T, srv *Server, secrets CookieSecrets, uid []byte) ([]byte, []byte, []byte) {
	t.Helper()
	cookie, err := srv.MintCookie(secrets)
	if err != nil {
		t.Fatalf("MintCookie: %v", err)
	}

	header := make([]byte, ntpHeaderSize)
	header[0] = (4 << 3) | 3 // VN=4, Mode=3(client)

	uidField := MarshalExtension(ExtUniqueIdentifier, uid)
	cookieField := MarshalExtension(ExtNTSCookie, cookie)

	aad := make([]byte, 0, len(header)+len(uidField)+len(cookieField))
	aad = append(aad, header...)
	aad = append(aad, uidField...)
	aad = append(aad, cookieField...)

	c2s, err := NewAEAD(secrets.AEAD, secrets.C2S)
	if err != nil {
		t.Fatalf("NewAEAD(c2s): %v", err)
	}
	authField, err := sealAuthenticator(c2s, secrets.AEAD, aad, nil)
	if err != nil {
		t.Fatalf("sealAuthenticator: %v", err)
	}

	packet := make([]byte, 0, len(aad)+len(authField))
	packet = append(packet, aad...)
	packet = append(packet, authField...)
	return packet, uid, cookie
}

// buildRequestWithCookie 用指定 UID 与既有 Cookie 重新封签一条请求。
func buildRequestWithCookie(t *testing.T, secrets CookieSecrets, uid, cookie []byte) []byte {
	t.Helper()
	header := make([]byte, ntpHeaderSize)
	header[0] = (4 << 3) | 3
	uidField := MarshalExtension(ExtUniqueIdentifier, uid)
	cookieField := MarshalExtension(ExtNTSCookie, cookie)
	aad := append(append(append([]byte{}, header...), uidField...), cookieField...)
	c2s, err := NewAEAD(secrets.AEAD, secrets.C2S)
	if err != nil {
		t.Fatalf("NewAEAD: %v", err)
	}
	authField, err := sealAuthenticator(c2s, secrets.AEAD, aad, nil)
	if err != nil {
		t.Fatalf("sealAuthenticator: %v", err)
	}
	return append(append([]byte{}, aad...), authField...)
}

// openClientResponse 模拟客户端验证服务器响应：
// S2C 密钥验证 Authenticator，并回传 UID 字段体与新 Cookie。
func openClientResponse(t *testing.T, secrets CookieSecrets, response []byte) (uidBody, newCookie []byte, err error) {
	t.Helper()
	if len(response) < ntpHeaderSize {
		return nil, nil, ErrPacketTooShort
	}
	exts, err := ParseExtensions(response[ntpHeaderSize:])
	if err != nil {
		return nil, nil, err
	}
	var authOffset int
	var auth Authenticator
	for i, ext := range exts {
		fieldSize := extHeaderSize + len(ext.Body)
		if ext.Type == ExtNTSAuthenticator {
			if i != len(exts)-1 {
				return nil, nil, ErrAuthenticatorOrder
			}
			auth, err = parseAuthenticator(ext.Body)
			if err != nil {
				return nil, nil, err
			}
			break
		}
		authOffset += fieldSize
	}
	s2c, err := NewAEAD(secrets.AEAD, secrets.S2C)
	if err != nil {
		return nil, nil, err
	}
	aad := response[:ntpHeaderSize+authOffset]
	if _, err := openAuthenticator(s2c, auth, aad); err != nil {
		return nil, nil, err
	}
	for _, ext := range exts {
		switch ext.Type {
		case ExtUniqueIdentifier:
			uidBody = ext.Body
		case ExtNTSCookie:
			newCookie = ext.Value
		}
	}
	return uidBody, newCookie, nil
}

func randomUID(t *testing.T, size int) []byte {
	t.Helper()
	uid := make([]byte, size)
	if _, err := rand.Read(uid); err != nil {
		t.Fatalf("rand uid: %v", err)
	}
	return uid
}
