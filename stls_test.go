package netsec

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	"net"
	"slices"
	"strings"
	"testing"
	"time"
)

// serverScript 在 net.Pipe 的服务器端执行一段脚本。
type serverScript func(t *testing.T, conn net.Conn, r *bufio.Reader)

// runServer 启动脚本化服务器并返回客户端侧连接。
func runServer(t *testing.T, script serverScript) net.Conn {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	go func() {
		defer serverConn.Close()
		script(t, serverConn, bufio.NewReader(serverConn))
	}()
	return clientConn
}

func expectCommand(t *testing.T, r *bufio.Reader, want string) {
	t.Helper()
	line, err := r.ReadString('\n')
	if err != nil {
		t.Errorf("server: reading command: %v", err)
		return
	}
	if got := strings.TrimRight(line, "\r\n"); got != want {
		t.Errorf("server: got command %q, want %q", got, want)
	}
}

func writeLines(t *testing.T, conn net.Conn, lines ...string) {
	t.Helper()
	for _, line := range lines {
		if _, err := conn.Write([]byte(line + "\r\n")); err != nil {
			t.Errorf("server: writing %q: %v", line, err)
			return
		}
	}
}

func newClient(t *testing.T, conn net.Conn) *Client {
	t.Helper()
	c, err := NewClient(conn)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

// TestUpgradeRefusedOnErr：STLS 得到 -ERR 必须失败并断开，
// 客户端不得回退到明文登录（不得再发出任何命令）。
func TestUpgradeRefusedOnErr(t *testing.T) {
	conn := runServer(t, func(t *testing.T, conn net.Conn, r *bufio.Reader) {
		writeLines(t, conn, "+OK POP3 server ready")
		expectCommand(t, r, "CAPA")
		writeLines(t, conn, "+OK Capability list follows", "STLS", "USER", ".")
		expectCommand(t, r, "STLS")
		writeLines(t, conn, "-ERR TLS not available")
		// 客户端必须关闭连接而不是继续明文登录。
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		line, err := r.ReadString('\n')
		if err == nil {
			t.Errorf("server: client sent plaintext command after -ERR: %q", strings.TrimRight(line, "\r\n"))
		} else if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrClosedPipe) {
			t.Errorf("server: expected closed connection, got %v", err)
		}
	})

	c := newClient(t, conn)
	err := c.Upgrade(&tls.Config{InsecureSkipVerify: true}) // 测试用，不校验证书
	if !errors.Is(err, ErrSTLSRefused) {
		t.Fatalf("Upgrade error = %v, want ErrSTLSRefused", err)
	}
	if c.Encrypted() {
		t.Fatal("connection must not be marked encrypted after refused STLS")
	}
}

// TestUpgradeNoSTLS：CAPA 列表没有 STLS 时必须失败。
func TestUpgradeNoSTLS(t *testing.T) {
	conn := runServer(t, func(t *testing.T, conn net.Conn, r *bufio.Reader) {
		writeLines(t, conn, "+OK POP3 server ready")
		expectCommand(t, r, "CAPA")
		writeLines(t, conn, "+OK Capability list follows", "USER", "TOP", ".")
	})

	c := newClient(t, conn)
	err := c.Upgrade(&tls.Config{InsecureSkipVerify: true})
	if !errors.Is(err, ErrNoSTLS) {
		t.Fatalf("Upgrade error = %v, want ErrNoSTLS", err)
	}
}

// TestUpgradeInjectedBytesAfterOK：+OK 之后读缓冲里残留的明文字节
// 视为注入，必须失败并断开。
func TestUpgradeInjectedBytesAfterOK(t *testing.T) {
	conn := runServer(t, func(t *testing.T, conn net.Conn, r *bufio.Reader) {
		writeLines(t, conn, "+OK POP3 server ready")
		expectCommand(t, r, "CAPA")
		writeLines(t, conn, "+OK Capability list follows", "STLS", ".")
		expectCommand(t, r, "STLS")
		// 一次写入中在 +OK 后夹带明文命令，模拟路径上的注入。
		if _, err := conn.Write([]byte("+OK Begin TLS\r\nUSER attacker@example.org\r\n")); err != nil {
			t.Errorf("server: writing poisoned reply: %v", err)
		}
	})

	c := newClient(t, conn)
	err := c.Upgrade(&tls.Config{InsecureSkipVerify: true})
	if !errors.Is(err, ErrInjectedBytes) {
		t.Fatalf("Upgrade error = %v, want ErrInjectedBytes", err)
	}
	if c.Encrypted() {
		t.Fatal("connection must not be marked encrypted after detected injection")
	}
}

// testServerCert 生成测试用的自签服务器证书。
func testServerCert(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "pop3.test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"pop3.test"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("creating certificate: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// upgradeServer 完成握手前脚本，随后作为 TLS 服务器握手并执行 postHandshake。
func upgradeServer(t *testing.T, conn net.Conn, r *bufio.Reader, preCaps []string, postHandshake func(t *testing.T, conn net.Conn, r *bufio.Reader)) {
	writeLines(t, conn, "+OK POP3 server ready")
	expectCommand(t, r, "CAPA")
	writeLines(t, conn, append([]string{"+OK Capability list follows"}, append(preCaps, ".")...)...)
	expectCommand(t, r, "STLS")
	writeLines(t, conn, "+OK Begin TLS")
	tlsConn := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{testServerCert(t)}})
	if err := tlsConn.Handshake(); err != nil {
		t.Errorf("server: TLS handshake: %v", err)
		return
	}
	postHandshake(t, tlsConn, bufio.NewReader(tlsConn))
}

// TestUpgradeDropsPreHandshakeCaps：握手前能力里的 USER 在新列表中被去除时，
// 客户端不得再依据旧列表选择 USER 认证。
func TestUpgradeDropsPreHandshakeCaps(t *testing.T) {
	conn := runServer(t, func(t *testing.T, conn net.Conn, r *bufio.Reader) {
		upgradeServer(t, conn, r, []string{"STLS", "USER", "TOP"},
			func(t *testing.T, conn net.Conn, r *bufio.Reader) {
				// 握手后的新能力列表不再包含 USER。
				expectCommand(t, r, "CAPA")
				writeLines(t, conn, "+OK Capability list follows", "SASL PLAIN", ".")
			})
	})

	c := newClient(t, conn)
	if err := c.Upgrade(&tls.Config{InsecureSkipVerify: true}); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	if !c.Encrypted() {
		t.Fatal("connection should be encrypted after upgrade")
	}
	if slices.Contains(c.AuthMethods(), "USER") {
		t.Fatalf("AuthMethods = %v, must not offer USER after new CAPA list dropped it", c.AuthMethods())
	}
	caps := c.Caps()
	if _, ok := caps["USER"]; ok {
		t.Fatal("stale pre-handshake USER capability still present")
	}
	if _, ok := caps["TOP"]; ok {
		t.Fatal("stale pre-handshake TOP capability still present")
	}
	if got := caps["SASL"]; !slices.Equal(got, []string{"PLAIN"}) {
		t.Fatalf("SASL capability = %v, want [PLAIN]", got)
	}
}

// TestUpgradeSuccess：合法升级——CAPA 以单独一行 "." 结束、STLS +OK、
// 缓冲干净、握手后重新获取能力并据此选择认证方式。
func TestUpgradeSuccess(t *testing.T) {
	conn := runServer(t, func(t *testing.T, conn net.Conn, r *bufio.Reader) {
		upgradeServer(t, conn, r, []string{"STLS", "USER", "TOP", "UIDL"},
			func(t *testing.T, conn net.Conn, r *bufio.Reader) {
				expectCommand(t, r, "CAPA")
				writeLines(t, conn, "+OK Capability list follows", "USER", "SASL PLAIN LOGIN", "UIDL", ".")
			})
	})

	c := newClient(t, conn)
	if err := c.Upgrade(&tls.Config{InsecureSkipVerify: true}); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	if !c.Encrypted() {
		t.Fatal("connection should be encrypted after upgrade")
	}
	caps := c.Caps()
	if _, ok := caps["USER"]; !ok {
		t.Fatal("post-handshake USER capability missing")
	}
	if _, ok := caps["TOP"]; ok {
		t.Fatal("pre-handshake TOP capability should have been discarded")
	}
	methods := c.AuthMethods()
	for _, want := range []string{"USER", "SASL PLAIN", "SASL LOGIN"} {
		if !slices.Contains(methods, want) {
			t.Fatalf("AuthMethods = %v, want it to contain %q", methods, want)
		}
	}
}
