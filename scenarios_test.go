package netsec

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

// TestValidRequestAcceptsAndEchoesUID 合法请求：通过认证、原样回显 UID、补发新 Cookie。
func TestValidRequestAcceptsAndEchoesUID(t *testing.T) {
	clock := newFakeClock()
	srv := newTestServer(t, clock)
	secrets, err := GenerateSecrets(AEADAESGCM128)
	if err != nil {
		t.Fatalf("GenerateSecrets: %v", err)
	}
	uid := randomUID(t, MinUniqueIdentifierSize)

	packet, rawUID, oldCookie := sealClientRequest(t, srv, secrets, uid)
	response, err := srv.ProcessPacket(packet)
	if err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
	if len(response)%4 != 0 || len(response) < ntpHeaderSize {
		t.Fatalf("response malformed: length=%d", len(response))
	}

	echoedUID, newCookie, err := openClientResponse(t, secrets, response)
	if err != nil {
		t.Fatalf("client cannot verify response authenticator: %v", err)
	}
	if !bytes.Equal(trimZeroPadding(echoedUID), rawUID) {
		t.Fatalf("unique identifier not echoed verbatim: got %x want %x", echoedUID, rawUID)
	}
	if len(newCookie) == 0 {
		t.Fatal("response does not carry a fresh nts cookie")
	}
	if bytes.Equal(newCookie, oldCookie) {
		t.Fatal("response reissued the same cookie instead of minting a fresh one")
	}
	recovered, err := srv.keyRing.Unwrap(newCookie)
	if err != nil {
		t.Fatalf("fresh cookie unwrap failed: %v", err)
	}
	if !bytes.Equal(recovered.C2S, secrets.C2S) || !bytes.Equal(recovered.S2C, secrets.S2C) {
		t.Fatal("fresh cookie does not wrap the same session secrets")
	}
	if response[0]&0x07 != 4 {
		t.Fatalf("response mode is not server(4): 0x%02x", response[0])
	}
}

// TestReplayedRequestRejected 重放请求：同一 UID 第二次出现必须被拒且不产生响应。
func TestReplayedRequestRejected(t *testing.T) {
	clock := newFakeClock()
	srv := newTestServer(t, clock)
	secrets, err := GenerateSecrets(AEADAESGCM128)
	if err != nil {
		t.Fatalf("GenerateSecrets: %v", err)
	}
	packet, _, _ := sealClientRequest(t, srv, secrets, randomUID(t, 32))

	if _, err := srv.ProcessPacket(packet); err != nil {
		t.Fatalf("first request should succeed: %v", err)
	}
	if _, err := srv.ProcessPacket(packet); !errors.Is(err, ErrReplayedRequest) {
		t.Fatalf("replay want %v, got %v", ErrReplayedRequest, err)
	}
	if _, err := srv.ProcessPacket(packet); !errors.Is(err, ErrReplayedRequest) {
		t.Fatalf("third replay want %v, got %v", ErrReplayedRequest, err)
	}
}

// TestReplayedRequestExpiresAfterTTL 超过防重放窗口后同 UID 不再被拦截。
func TestReplayedRequestExpiresAfterTTL(t *testing.T) {
	clock := newFakeClock()
	srv := newTestServer(t, clock)
	secrets, err := GenerateSecrets(AEADAESGCM128)
	if err != nil {
		t.Fatalf("GenerateSecrets: %v", err)
	}
	uid := randomUID(t, 32)
	first, _, _ := sealClientRequest(t, srv, secrets, uid)
	if _, err := srv.ProcessPacket(first); err != nil {
		t.Fatalf("first request: %v", err)
	}
	clock.advance(time.Minute + time.Second)

	second, _, _ := sealClientRequest(t, srv, secrets, uid)
	if _, err := srv.ProcessPacket(second); err != nil {
		t.Fatalf("request after ttl should be accepted again, got %v", err)
	}
}

// TestCookieReuseRejected 复用 Cookie：不同 UID 的请求共用同一 Cookie，第二次拒绝。
func TestCookieReuseRejected(t *testing.T) {
	clock := newFakeClock()
	srv := newTestServer(t, clock)
	secrets, err := GenerateSecrets(AEADAESGCM128)
	if err != nil {
		t.Fatalf("GenerateSecrets: %v", err)
	}

	firstPacket, _, sharedCookie := sealClientRequest(t, srv, secrets, randomUID(t, 32))
	if _, err := srv.ProcessPacket(firstPacket); err != nil {
		t.Fatalf("first cookie use should succeed: %v", err)
	}

	secondPacket := buildRequestWithCookie(t, secrets, randomUID(t, 48), sharedCookie)
	if _, err := srv.ProcessPacket(secondPacket); !errors.Is(err, ErrReusedCookie) {
		t.Fatalf("cookie reuse want %v, got %v", ErrReusedCookie, err)
	}
}

// TestTamperedUIDRejected 改 UID：Authenticator 的 AAD 覆盖 UID，篡改后认证失败、不回复。
func TestTamperedUIDRejected(t *testing.T) {
	clock := newFakeClock()
	srv := newTestServer(t, clock)
	secrets, err := GenerateSecrets(AEADAESGCM128)
	if err != nil {
		t.Fatalf("GenerateSecrets: %v", err)
	}
	packet, _, _ := sealClientRequest(t, srv, secrets, randomUID(t, 32))

	tampered := append([]byte(nil), packet...)
	tampered[ntpHeaderSize+extHeaderSize] ^= 0xFF // UID 字段体首字节翻转
	if _, err := srv.ProcessPacket(tampered); !errors.Is(err, ErrAuthenticationFailed) {
		t.Fatalf("tampered uid want %v, got %v", ErrAuthenticationFailed, err)
	}
}

// TestTamperedHeaderRejected 篡改 NTP 头（模式字段）同样必须认证失败。
func TestTamperedHeaderRejected(t *testing.T) {
	clock := newFakeClock()
	srv := newTestServer(t, clock)
	secrets, err := GenerateSecrets(AEADAESGCM128)
	if err != nil {
		t.Fatalf("GenerateSecrets: %v", err)
	}
	packet, _, _ := sealClientRequest(t, srv, secrets, randomUID(t, 32))

	tampered := append([]byte(nil), packet...)
	tampered[0] ^= 0x07 // 改动 Mode 位
	if _, err := srv.ProcessPacket(tampered); !errors.Is(err, ErrAuthenticationFailed) {
		t.Fatalf("tampered header want %v, got %v", ErrAuthenticationFailed, err)
	}
}

// TestTamperedAuthenticatorRejected 损坏 Authenticator 密文也必须被拒。
func TestTamperedAuthenticatorRejected(t *testing.T) {
	clock := newFakeClock()
	srv := newTestServer(t, clock)
	secrets, err := GenerateSecrets(AEADAESGCM128)
	if err != nil {
		t.Fatalf("GenerateSecrets: %v", err)
	}
	packet, _, _ := sealClientRequest(t, srv, secrets, randomUID(t, 32))

	tampered := append([]byte(nil), packet...)
	tagPos := lastAuthenticatorTagByte(t, packet)
	tampered[tagPos] ^= 0x01 // GCM 标签末位翻转
	if _, err := srv.ProcessPacket(tampered); !errors.Is(err, ErrAuthenticationFailed) {
		t.Fatalf("tampered authenticator want %v, got %v", ErrAuthenticationFailed, err)
	}
}

// lastAuthenticatorTagByte 定位 Authenticator 密文（含 GCM 标签）最后一个有效字节，
// 避免翻转到扩展字段的 4 字节对齐填充上。
func lastAuthenticatorTagByte(t *testing.T, packet []byte) int {
	t.Helper()
	exts, err := ParseExtensions(packet[ntpHeaderSize:])
	if err != nil {
		t.Fatalf("ParseExtensions: %v", err)
	}
	offset := ntpHeaderSize
	for _, ext := range exts {
		fieldSize := extHeaderSize + len(ext.Body)
		if ext.Type == ExtNTSAuthenticator {
			auth, err := parseAuthenticator(ext.Body)
			if err != nil {
				t.Fatalf("parseAuthenticator: %v", err)
			}
			return offset + extHeaderSize + 6 + len(auth.Nonce) + len(auth.Ciphertext) - 1
		}
		offset += fieldSize
	}
	t.Fatal("authenticator extension not found")
	return -1
}

// TestShortUIDRejected UID 不足 32 字节在结构校验阶段拒绝。
func TestShortUIDRejected(t *testing.T) {
	clock := newFakeClock()
	srv := newTestServer(t, clock)
	secrets, err := GenerateSecrets(AEADAESGCM128)
	if err != nil {
		t.Fatalf("GenerateSecrets: %v", err)
	}
	packet, _, _ := sealClientRequest(t, srv, secrets, randomUID(t, MinUniqueIdentifierSize-1))

	if _, err := srv.ProcessPacket(packet); !errors.Is(err, ErrUniqueIdentifierTooShort) {
		t.Fatalf("short uid want %v, got %v", ErrUniqueIdentifierTooShort, err)
	}
}

// TestUIDFieldEchoedByteForByte UID 长度非 4 倍数时带填充，
// 响应中的 UID 扩展字段必须与请求线上字节完全一致。
func TestUIDFieldEchoedByteForByte(t *testing.T) {
	clock := newFakeClock()
	srv := newTestServer(t, clock)
	secrets, err := GenerateSecrets(AEADAESGCM128)
	if err != nil {
		t.Fatalf("GenerateSecrets: %v", err)
	}
	// 33 字节 UID：字段体 33 字节，按 4 字节对齐后字段总长 40，含 3 字节零填充。
	packet, _, _ := sealClientRequest(t, srv, secrets, randomUID(t, 33))
	response, err := srv.ProcessPacket(packet)
	if err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}

	reqUIDField := packet[ntpHeaderSize : ntpHeaderSize+extHeaderSize+36] // 4 + 36
	respUIDField := response[ntpHeaderSize : ntpHeaderSize+extHeaderSize+36]
	if !bytes.Equal(reqUIDField, respUIDField) {
		t.Fatalf("uid extension not echoed byte-for-byte\n request:  %x\n response: %x", reqUIDField, respUIDField)
	}
}

// TestMalformedPacketsRejected 各类畸形报文均返回错误且无响应。
func TestMalformedPacketsRejected(t *testing.T) {
	clock := newFakeClock()
	srv := newTestServer(t, clock)

	cases := map[string][]byte{
		"too short":              make([]byte, ntpHeaderSize-1),
		"bad alignment":          append(make([]byte, ntpHeaderSize), 0x01, 0x04),
		"unknown extension type": buildUnknownTypePacket(t),
		"forged cookie":          buildForgedCookiePacket(t, srv),
	}
	for name, packet := range cases {
		t.Run(name, func(t *testing.T) {
			if response, err := srv.ProcessPacket(packet); err == nil {
				t.Fatalf("%s: expected error, got response of %d bytes", name, len(response))
			}
		})
	}
}

func buildUnknownTypePacket(t *testing.T) []byte {
	t.Helper()
	packet := make([]byte, ntpHeaderSize)
	packet = append(packet, MarshalExtension(0x0707, make([]byte, 4))...)
	return packet
}

func buildForgedCookiePacket(t *testing.T, srv *Server) []byte {
	t.Helper()
	secrets, err := GenerateSecrets(AEADAESGCM128)
	if err != nil {
		t.Fatalf("GenerateSecrets: %v", err)
	}
	packet, _, _ := sealClientRequest(t, srv, secrets, randomUID(t, 32))
	// 找到 Cookie 字段并整体替换为无法解封的伪造字节。
	exts, err := ParseExtensions(packet[ntpHeaderSize:])
	if err != nil {
		t.Fatalf("ParseExtensions: %v", err)
	}
	offset := ntpHeaderSize
	for _, ext := range exts {
		fieldSize := extHeaderSize + len(ext.Body)
		if ext.Type == ExtNTSCookie {
			for i := offset + extHeaderSize; i < offset+fieldSize; i++ {
				packet[i] = 0x5A
			}
			break
		}
		offset += fieldSize
	}
	return packet
}
