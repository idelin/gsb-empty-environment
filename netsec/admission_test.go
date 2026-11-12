package netsec

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"testing"
	"time"
)

// packetize 给载荷加 4 字节 MySQL 包头。
func packetize(seq byte, payload []byte) []byte {
	frame := make([]byte, 4+len(payload))
	frame[0] = byte(len(payload))
	frame[1] = byte(len(payload) >> 8)
	frame[2] = byte(len(payload) >> 16)
	frame[3] = seq
	copy(frame[4:], payload)
	return frame
}

// buildHandshakePayload 构造协议版本 10 的初始握手包载荷，
// scramble 固定 20 字节，auth-plugin-data-len 固定 21。
func buildHandshakePayload(caps uint32, charset byte) []byte {
	var buf bytes.Buffer
	buf.WriteByte(10)
	buf.WriteString("8.0.32-test")
	buf.WriteByte(0)
	_ = binary.Write(&buf, binary.LittleEndian, uint32(0x7123abcd))
	scramble1 := make([]byte, 8)
	for i := range scramble1 {
		scramble1[i] = byte(i + 1)
	}
	buf.Write(scramble1)
	buf.WriteByte(0)
	_ = binary.Write(&buf, binary.LittleEndian, uint16(caps))
	buf.WriteByte(charset)
	_ = binary.Write(&buf, binary.LittleEndian, uint16(0x0002))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(caps>>16))
	buf.WriteByte(21)
	buf.Write(make([]byte, 10))
	scramble2 := make([]byte, 13) // 21 - 8
	for i := range scramble2 {
		scramble2[i] = byte(i + 9)
	}
	scramble2[12] = 0
	buf.Write(scramble2)
	buf.WriteString("mysql_native_password")
	buf.WriteByte(0)
	return buf.Bytes()
}

// recordingConn 记录明文阶段所有出站字节，同时透传底层连接。
type recordingConn struct {
	net.Conn
	writes      bytes.Buffer
	writeCount  int
	plainBytes  int
	plainWrites int
}

func (c *recordingConn) Write(p []byte) (int, error) {
	c.writeCount++
	c.writes.Write(p)
	// 明文阶段的唯一出站是首个写（36 字节 SSLRequest）；
	// 此后的所有写均为 TLS 加密记录，不计入明文统计。
	if c.writeCount == 1 {
		c.plainBytes += len(p)
		c.plainWrites++
	}
	return c.Conn.Write(p)
}

// requireConfig 构造要求 TLS 的准入配置，测试使用非阻塞探测。
func requireConfig() Config {
	return Config{
		RequireTLS:   true,
		TLSConfig:    &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12},
		ProbeTimeout: -1,
	}
}

const fullServerCaps = capProtocol41 | capSecureConnection | capClientSSL | capPluginAuth

// TestSSLStripped 中间人剥掉 CLIENT_SSL：必须以 ErrTLSRequired 中止，
// 且连接上没有任何出站字节。
func TestSSLStripped(t *testing.T) {
	client, server := newMemoryPipe()
	defer server.Close()

	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		stripped := capProtocol41 | capSecureConnection | capPluginAuth // 不含 capClientSSL
		_, _ = server.Write(packetize(0, buildHandshakePayload(stripped, 45)))
	}()

	rec := &recordingConn{Conn: client}
	_, err := Begin(rec, requireConfig())
	if !errors.Is(err, ErrTLSRequired) {
		t.Fatalf("expected ErrTLSRequired, got %v", err)
	}
	if rec.writes.Len() != 0 {
		t.Fatalf("no bytes may be written when SSL is stripped, got %d", rec.writes.Len())
	}
	<-serverDone
	_ = client.Close()
}

// TestPrematureBytes SSLRequest 后、TLS 前夹带明文字节：必须中止。
func TestPrematureBytes(t *testing.T) {
	t.Run("extra packet appended to initial handshake", func(t *testing.T) {
		client, server := newMemoryPipe()
		defer server.Close()

		go func() {
			frame := packetize(0, buildHandshakePayload(fullServerCaps, 45))
			// 中间人在初始握手包后追加伪造 ERR 包（序号 1）。
			frame = append(frame, packetize(1,
				[]byte{0xff, 0x10, 0x04, 'b', 'o', 'g', 'u', 's'})...)
			_, _ = server.Write(frame)
		}()

		_, err := Begin(&recordingConn{Conn: client}, requireConfig())
		if !errors.Is(err, ErrPrematureBytes) {
			t.Fatalf("expected ErrPrematureBytes for appended packet, got %v", err)
		}
		_ = client.Close()
	})

	t.Run("plaintext byte injected after SSLRequest", func(t *testing.T) {
		// 内存管道具备套接字式的读截止时间唤醒语义。
		client, server := newMemoryPipe()
		defer server.Close()
		go func() {
			_, _ = server.Write(packetize(0, buildHandshakePayload(fullServerCaps, 45)))
			hdr := make([]byte, 4)
			if _, err := io.ReadFull(server, hdr); err != nil {
				return
			}
			body := make([]byte, int(hdr[0])|int(hdr[1])<<8|int(hdr[2])<<16)
			if _, err := io.ReadFull(server, body); err != nil {
				return
			}
			// 中间人在 TLS 握手开始前注入一个明文字节。
			_, _ = server.Write([]byte{0x00})
		}()

		rec := &recordingConn{Conn: client}
		cfg := requireConfig()
		// 给服务端 goroutine 留出写入注入字节的窗口，消除调度竞态。
		cfg.ProbeTimeout = 20 * time.Millisecond
		pending, err := Begin(rec, cfg)
		if err != nil {
			t.Fatalf("Begin failed: %v", err)
		}
		if _, err := pending.Upgrade(context.Background()); !errors.Is(err, ErrPrematureBytes) {
			t.Fatalf("expected ErrPrematureBytes after SSLRequest, got %v", err)
		}
		// 明文阶段唯一出站报文必须是 36 字节 SSLRequest（4 头 + 32 载荷）。
		if got := rec.writes.Len(); got != 36 {
			t.Fatalf("plaintext write must be exactly 36 bytes, got %d", got)
		}
		_ = client.Close()
	})
}

// TestOutOfOrderPacket 乱序/非法序号包必须被拒绝。
func TestOutOfOrderPacket(t *testing.T) {
	t.Run("initial packet sequence not zero", func(t *testing.T) {
		client, server := newMemoryPipe()
		defer server.Close()

		go func() {
			_, _ = server.Write(packetize(7, buildHandshakePayload(fullServerCaps, 45)))
		}()

		_, err := Begin(&recordingConn{Conn: client}, requireConfig())
		if !errors.Is(err, ErrOutOfOrderPacket) {
			t.Fatalf("expected ErrOutOfOrderPacket, got %v", err)
		}
		_ = client.Close()
	})

	t.Run("two packets before any client response", func(t *testing.T) {
		client, server := newMemoryPipe()
		defer server.Close()

		go func() {
			frame := packetize(0, buildHandshakePayload(fullServerCaps, 45))
			frame = append(frame, packetize(1, make([]byte, 8))...)
			_, _ = server.Write(frame)
		}()

		_, err := Begin(&recordingConn{Conn: client}, requireConfig())
		if !errors.Is(err, ErrPrematureBytes) {
			t.Fatalf("expected ErrPrematureBytes for second packet, got %v", err)
		}
		_ = client.Close()
	})
}

// TestMalformedHandshake 畸形握手包必须被明确拒绝。
func TestMalformedHandshake(t *testing.T) {
	client, server := newMemoryPipe()
	defer server.Close()

	go func() {
		_, _ = server.Write(packetize(0, []byte{9, 'o', 'l', 'd'}))
	}()

	_, err := Begin(&recordingConn{Conn: client}, requireConfig())
	if !errors.Is(err, ErrMalformedHandshake) {
		t.Fatalf("expected ErrMalformedHandshake, got %v", err)
	}
	_ = client.Close()
}

// TestAuthMaterialSealedBeforeTLS 明文阶段认证材料必须处于封存状态。
func TestAuthMaterialSealedBeforeTLS(t *testing.T) {
	client, server := newMemoryPipe()
	defer server.Close()

	go func() {
		_, _ = server.Write(packetize(0, buildHandshakePayload(fullServerCaps, 45)))
	}()

	pending, err := Begin(&recordingConn{Conn: client}, requireConfig())
	if err != nil {
		t.Fatalf("Begin failed: %v", err)
	}
	if _, err := pending.AuthPluginData(); !errors.Is(err, ErrTLSNotEstablished) {
		t.Fatalf("expected ErrTLSNotEstablished in plaintext phase, got %v", err)
	}
	_ = client.Close()
}

// TestTLSIntegration 正向端到端：真实 TCP + TLS 回环，验证 SSLRequest
// 序号/长度、明文零认证泄露，以及 TLS 建立后才能取得 scramble。
func TestTLSIntegration(t *testing.T) {
	caCert, tlsCert, serverName := mustSelfSignedCert(t)

	clientRaw, serverRaw := newMemoryPipe()
	defer serverRaw.Close()
	serverErr := make(chan error, 1)
	go func() {
		raw := serverRaw
		defer raw.Close()

		if _, err := raw.Write(packetize(0, buildHandshakePayload(fullServerCaps, 45))); err != nil {
			serverErr <- err
			return
		}

		// 校验 SSLRequest：序号 1、载荷 32 字节、含 CLIENT_SSL。
		hdr := make([]byte, 4)
		if _, err := io.ReadFull(raw, hdr); err != nil {
			serverErr <- err
			return
		}
		if hdr[3] != 1 {
			serverErr <- errors.New("server: SSLRequest sequence must be 1")
			return
		}
		bodyLen := int(hdr[0]) | int(hdr[1])<<8 | int(hdr[2])<<16
		if bodyLen != 32 {
			serverErr <- errors.New("server: SSLRequest payload must be 32 bytes")
			return
		}
		body := make([]byte, bodyLen)
		if _, err := io.ReadFull(raw, body); err != nil {
			serverErr <- err
			return
		}
		if binary.LittleEndian.Uint32(body[0:4])&capClientSSL == 0 {
			serverErr <- errors.New("server: SSLRequest missing CLIENT_SSL")
			return
		}

		tlsConn := tls.Server(raw, &tls.Config{
			Certificates: []tls.Certificate{tlsCert},
			MinVersion:   tls.VersionTLS12,
		})
		if err := tlsConn.Handshake(); err != nil {
			serverErr <- err
			return
		}
		defer tlsConn.Close()

		// TLS 建立后读取加密 HandshakeResponse（序号应为 2）。
		respHdr := make([]byte, 4)
		if _, err := io.ReadFull(tlsConn, respHdr); err != nil {
			serverErr <- err
			return
		}
		if respHdr[3] != 2 {
			serverErr <- errors.New("server: HandshakeResponse sequence must be 2")
			return
		}
		respLen := int(respHdr[0]) | int(respHdr[1])<<8 | int(respHdr[2])<<16
		resp := make([]byte, respLen)
		if _, err := io.ReadFull(tlsConn, resp); err != nil {
			serverErr <- err
			return
		}
		if !bytes.Contains(resp, []byte("alice")) {
			serverErr <- errors.New("server: encrypted response missing username")
			return
		}
		serverErr <- nil
	}()

	rootPool := x509.NewCertPool()
	rootPool.AddCert(caCert)

	// 记录器作为最外层：bufferedConn 的读写都先经过它，
	// 因此 MarkTLS 前的字节即严格的明文阶段流量。
	rec := &recordingConn{Conn: clientRaw}

	cfg := Config{
		RequireTLS: true,
		TLSConfig: &tls.Config{
			RootCAs:    rootPool,
			ServerName: serverName,
			MinVersion: tls.VersionTLS12,
		},
		// 短暂窗口验证静默对端不会被误判，超时后正常进入 TLS 握手。
		ProbeTimeout: 20 * time.Millisecond,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	secure, err := Admit(ctx, rec, cfg)
	if err != nil {
		t.Fatalf("Admit failed: %v", err)
	}

	// 明文阶段只允许一个 36 字节 SSLRequest，且不含用户名/scramble。
	if rec.plainBytes != 36 || rec.plainWrites != 1 {
		t.Fatalf("plaintext phase must contain one 36-byte SSLRequest, got %d bytes in %d writes",
			rec.plainBytes, rec.plainWrites)
	}
	plain := rec.writes.Bytes()[:rec.plainBytes]
	if bytes.Contains(plain, []byte("alice")) || bytes.Contains(plain, []byte("scramble-secret")) {
		t.Fatalf("auth material leaked into plaintext: %x", plain)
	}

	// TLS 建立后认证材料才可用，且与握手包 scramble 一致（20 字节）。
	scramble := secure.AuthPluginData()
	wantScramble := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20}
	if !bytes.Equal(scramble, wantScramble) {
		t.Fatalf("scramble mismatch after TLS: got %v", scramble)
	}

	// 在加密通道上发送 HandshakeResponse（序号 2），服务端可解密读到用户名。
	authResp := buildHandshakeResponse41(secure.ServerCapabilities(), "alice", scramble)
	if err := writePacket(secure, 2, authResp); err != nil {
		t.Fatalf("write encrypted response: %v", err)
	}

	if err := <-serverErr; err != nil {
		t.Fatalf("server side validation failed: %v", err)
	}
}

// buildHandshakeResponse41 构造最小可用的 HandshakeResponse41 载荷，
// 仅用于测试中证明认证响应走的是加密通道。
func buildHandshakeResponse41(caps uint32, user string, scramble []byte) []byte {
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.LittleEndian, caps&clientCapabilityMask)
	_ = binary.Write(&buf, binary.LittleEndian, uint32(defaultMaxPacketSize))
	buf.WriteByte(defaultCharset)
	buf.Write(make([]byte, 23))
	buf.WriteString(user)
	buf.WriteByte(0)
	auth := nativePasswordResponse("secret-password", scramble)
	buf.WriteByte(byte(len(auth)))
	buf.Write(auth)
	return buf.Bytes()
}

// mustSelfSignedCert 生成内存中的自签证书，返回 CA、服务端证书与主机名。
func mustSelfSignedCert(t *testing.T) (*x509.Certificate, tls.Certificate, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatalf("serial: %v", err)
	}
	tmpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "mysql-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:              []string{"mysql-test"},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	caCert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key)})
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("keypair: %v", err)
	}
	return caCert, tlsCert, "mysql-test"
}
