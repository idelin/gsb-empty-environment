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
	"fmt"
	"math/big"
	"net"
	"net/textproto"
	"testing"
	"time"
)

func testTLSConfigs(t *testing.T) (serverCfg, clientCfg *tls.Config) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "localhost"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	serverCfg = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	clientCfg = &tls.Config{ServerName: "localhost", RootCAs: pool}
	return serverCfg, clientCfg
}

// pipeClient 建立 net.Pipe 连接并完成问候，返回客户端与服务器端连接。
func pipeClient(t *testing.T) (*Client, net.Conn) {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	go fmt.Fprintf(serverConn, "+OK POP3 server ready\r\n")
	c, err := NewClient(clientConn)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() {
		c.Close()
		serverConn.Close()
	})
	return c, serverConn
}

func readCommand(t *testing.T, tr *textproto.Reader, want string) {
	t.Helper()
	line, err := tr.ReadLine()
	if err != nil {
		t.Fatalf("server: reading %s: %v", want, err)
	}
	if line != want {
		t.Fatalf("server: got command %q, want %q", line, want)
	}
}

// CAPA 多行应答以单独一行 "." 结束；列表中没有 STLS 时必须失败。
func TestUpgradeFailsWithoutSTLS(t *testing.T) {
	c, serverConn := pipeClient(t)
	go func() {
		tr := textproto.NewReader(bufio.NewReader(serverConn))
		readCommand(t, tr, "CAPA")
		fmt.Fprintf(serverConn, "+OK Capability list follows\r\nUSER\r\nAPOP\r\n.\r\n")
	}()
	err := c.Upgrade(&tls.Config{InsecureSkipVerify: true})
	if !errors.Is(err, ErrTLSRequired) {
		t.Fatalf("Upgrade error = %v, want ErrTLSRequired", err)
	}
}

// STLS 得到 -ERR 必须失败并断开，而不是继续明文登录。
func TestUpgradeSTLSRefused(t *testing.T) {
	c, serverConn := pipeClient(t)
	serverErr := make(chan error, 1)
	go func() {
		tr := textproto.NewReader(bufio.NewReader(serverConn))
		readCommand(t, tr, "CAPA")
		fmt.Fprintf(serverConn, "+OK\r\nSTLS\r\nUSER\r\n.\r\n")
		readCommand(t, tr, "STLS")
		fmt.Fprintf(serverConn, "-ERR TLS not available\r\n")
		// 客户端若回退为明文登录会继续发命令；此处应因连接被关闭而读失败。
		serverConn.SetReadDeadline(time.Now().Add(5 * time.Second))
		line, err := tr.ReadLine()
		if err == nil {
			serverErr <- fmt.Errorf("server received plaintext command after -ERR: %q", line)
			return
		}
		serverErr <- nil
	}()
	err := c.Upgrade(&tls.Config{InsecureSkipVerify: true})
	if !errors.Is(err, ErrSTLSRefused) {
		t.Fatalf("Upgrade error = %v, want ErrSTLSRefused", err)
	}
	if serr := <-serverErr; serr != nil {
		t.Fatal(serr)
	}
}

// STLS 的 +OK 之后读缓冲里夹带明文字节，视为注入并断开。
func TestUpgradeDetectsInjectedBytes(t *testing.T) {
	c, serverConn := pipeClient(t)
	go func() {
		tr := textproto.NewReader(bufio.NewReader(serverConn))
		readCommand(t, tr, "CAPA")
		fmt.Fprintf(serverConn, "+OK\r\nSTLS\r\n.\r\n")
		readCommand(t, tr, "STLS")
		// 攻击者在 +OK 之后夹带尚未交给 TLS 的明文字节。
		fmt.Fprintf(serverConn, "+OK Begin TLS\r\nUSER attacker@example.com\r\n")
	}()
	err := c.Upgrade(&tls.Config{InsecureSkipVerify: true})
	if !errors.Is(err, ErrInjectedBytes) {
		t.Fatalf("Upgrade error = %v, want ErrInjectedBytes", err)
	}
}

// serveTLSUpgrade 模拟合法 STLS 服务器：preCaps 为握手前能力，postCaps 为握手后能力。
func serveTLSUpgrade(t *testing.T, serverConn net.Conn, serverCfg *tls.Config, preCaps, postCaps string) {
	t.Helper()
	go func() {
		defer serverConn.Close()
		tr := textproto.NewReader(bufio.NewReader(serverConn))
		readCommand(t, tr, "CAPA")
		fmt.Fprintf(serverConn, "+OK\r\n%s.\r\n", preCaps)
		readCommand(t, tr, "STLS")
		fmt.Fprintf(serverConn, "+OK Begin TLS\r\n")
		tlsConn := tls.Server(serverConn, serverCfg)
		if err := tlsConn.Handshake(); err != nil {
			t.Errorf("server: TLS handshake: %v", err)
			return
		}
		tr = textproto.NewReader(bufio.NewReader(tlsConn))
		readCommand(t, tr, "CAPA")
		fmt.Fprintf(tlsConn, "+OK\r\n%s.\r\n", postCaps)
	}()
}

// 握手前能力里的 USER 被握手后的新列表去除，客户端不得再选用 USER。
func TestCapabilitiesRefreshedAfterUpgrade(t *testing.T) {
	serverCfg, clientCfg := testTLSConfigs(t)
	c, serverConn := pipeClient(t)
	serveTLSUpgrade(t, serverConn, serverCfg, "STLS\r\nUSER\r\n", "APOP\r\nUIDL\r\n")
	if err := c.Upgrade(clientCfg); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	if c.HasCapability("USER") {
		t.Fatal("stale pre-handshake USER capability survived the upgrade")
	}
	mech, err := c.SelectAuth()
	if err != nil {
		t.Fatalf("SelectAuth: %v", err)
	}
	if mech != "APOP" {
		t.Fatalf("SelectAuth = %q, want APOP from post-handshake capabilities", mech)
	}
}

// 合法升级：CAPA -> STLS -> TLS 握手 -> 重新 CAPA，新能力列表生效。
func TestLegitimateUpgrade(t *testing.T) {
	serverCfg, clientCfg := testTLSConfigs(t)
	c, serverConn := pipeClient(t)
	serveTLSUpgrade(t, serverConn, serverCfg, "STLS\r\nUSER\r\n", "USER\r\nAPOP\r\nUIDL\r\n")
	if err := c.Upgrade(clientCfg); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	if !c.HasCapability("USER") || !c.HasCapability("UIDL") {
		t.Fatal("post-handshake capabilities not applied")
	}
	mech, err := c.SelectAuth()
	if err != nil {
		t.Fatalf("SelectAuth: %v", err)
	}
	if mech != "APOP" {
		t.Fatalf("SelectAuth = %q, want APOP", mech)
	}
}
