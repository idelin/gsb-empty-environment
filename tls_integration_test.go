package streamguard_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"testing"
	"time"

	sg "formal.local/w27-8ca5752422"
)

func testSelfSignedCertificate(t *testing.T) tls.Certificate {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
	}

	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	return tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  key,
	}
}

func TestGuardUpgradeServerConnCompletesTLSAndCleanStream(t *testing.T) {
	serverRaw, clientRaw := net.Pipe()
	defer serverRaw.Close()
	defer clientRaw.Close()

	guard := sg.NewGuard(sg.DefaultConfig())
	smuggled := `<message id="evil"><body>OPEN_PUMP</body></message>`
	attack := testPlainStreamOpen + testStartTLS + smuggled

	if _, err := guard.Feed([]byte(attack)); err != nil {
		t.Fatal(err)
	}
	if guard.Buffered() != len(smuggled) {
		t.Fatalf("expected smuggled pre-TLS bytes, buffered=%d", guard.Buffered())
	}
	cert := testSelfSignedCertificate(t)

	serverResult := make(chan struct {
		conn *tls.Conn
		err  error
	}, 1)
	go func() {
		tlsConn, err := guard.UpgradeServerConn(serverRaw, &tls.Config{
			Certificates: []tls.Certificate{cert},
		})
		serverResult <- struct {
			conn *tls.Conn
			err  error
		}{tlsConn, err}
	}()

	clientTLS := tls.Client(clientRaw, &tls.Config{
		InsecureSkipVerify: true,
		ServerName:         "localhost",
	})
	if err := clientTLS.Handshake(); err != nil {
		t.Fatalf("client TLS handshake: %v", err)
	}

	result := <-serverResult
	if result.err != nil {
		t.Fatalf("server TLS upgrade: %v", result.err)
	}
	serverTLS := result.conn

	if guard.Buffered() != 0 {
		t.Fatalf("pre-TLS buffer survived TLS upgrade: %d bytes", guard.Buffered())
	}
	if guard.State() != sg.StateResetBufferAndStream {
		t.Fatalf("unexpected state after TLS upgrade: %s", guard.State())
	}

	postTLS := []byte(testSecureStreamOpen + testAuth)
	go func() {
		_, _ = clientTLS.Write(postTLS)
	}()

	encrypted := make([]byte, 4096)
	n, err := serverTLS.Read(encrypted)
	if err != nil {
		t.Fatalf("read decrypted post-TLS frame: %v", err)
	}
	if _, err := guard.Feed(encrypted[:n]); err != nil {
		t.Fatalf("feed post-TLS bytes: %v", err)
	}
	if guard.State() != sg.StateSASLAuth {
		t.Fatalf("expected encrypted stream to reach SASLAuth, got %s", guard.State())
	}
}
