package starttls_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"testing"
	"time"

	"example.org/netsec/uid1257/starttls"
)

func testCertificate(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "starttls-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("key pair: %v", err)
	}
	return cert
}

// TestRealTLSUpgradeAndPostSession runs an Engine in front of a real
// crypto/tls server over net.Pipe, then exchanges commands on the encrypted
// session. It proves the real TLS stack receives only handshake bytes and
// that post-upgrade lines never touch the plaintext connection.
func TestRealTLSUpgradeAndPostSession(t *testing.T) {
	cert := testCertificate(t)
	serverRaw, clientRaw := net.Pipe()

	serverEng, err := starttls.NewEngine(starttls.Config{Conn: serverRaw})
	if err != nil {
		t.Fatalf("server NewEngine: %v", err)
	}
	t.Cleanup(func() {
		_ = serverEng.Close()
		_ = clientRaw.Close()
		_ = serverRaw.Close()
	})

	type result struct {
		line string
		err  error
	}
	clientDone := make(chan result, 2)

	// Client side: plaintext STARTTLS, then a real tls.Client on the same
	// pipe, then an encrypted command.
	go func() {
		var tlsConn *tls.Conn
		defer func() {
			if tlsConn != nil {
				_ = tlsConn.Close()
			}
		}()
		if _, err := clientRaw.Write([]byte("STARTTLS\r\n")); err != nil {
			clientDone <- result{err: err}
			return
		}
		buf := make([]byte, 128)
		n, err := clientRaw.Read(buf)
		if err != nil {
			clientDone <- result{err: err}
			return
		}
		if string(buf[:n]) != "220 Go ahead\r\n" {
			clientDone <- result{err: errors.New("bad go-ahead: " + string(buf[:n]))}
			return
		}
		tlsConn = tls.Client(clientRaw, &tls.Config{
			InsecureSkipVerify: true,
			Certificates:       []tls.Certificate{cert},
			MinVersion:         tls.VersionTLS12,
		})
		if err := tlsConn.Handshake(); err != nil {
			clientDone <- result{err: err}
			return
		}
		if _, err := tlsConn.Write([]byte("MAIL FROM:<ok@example>\r\n")); err != nil {
			clientDone <- result{err: err}
			return
		}
		if _, err := tlsConn.Write([]byte("QUIT\r\n")); err != nil {
			clientDone <- result{err: err}
			return
		}
		reply := make([]byte, 64)
		n, err = tlsConn.Read(reply)
		clientDone <- result{line: string(reply[:n]), err: err}
	}()

	// Server side driven entirely through the Engine.
	cmd, err := serverEng.ReadLine()
	if err != nil || cmd != "STARTTLS" {
		t.Fatalf("server ReadLine = %q, %v", cmd, err)
	}
	if err := serverEng.WriteLine("220 Go ahead"); err != nil {
		t.Fatalf("go-ahead: %v", err)
	}

	upgrade := func(_ context.Context, raw starttls.DuplexConn) (starttls.DuplexConn, error) {
		tlsServer := tls.Server(raw.(net.Conn), &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		})
		if err := tlsServer.Handshake(); err != nil {
			return nil, err
		}
		return tlsServer, nil
	}
	if err := serverEng.BeginUpgrade(context.Background(), upgrade); err != nil {
		t.Fatalf("BeginUpgrade: %v", err)
	}
	if serverEng.Phase() != starttls.TLS {
		t.Fatalf("phase = %s, want tls", serverEng.Phase())
	}

	post, err := serverEng.ReadLine()
	if err != nil || post != "MAIL FROM:<ok@example>" {
		t.Fatalf("encrypted command = %q, %v", post, err)
	}
	quit, err := serverEng.ReadLine()
	if err != nil || quit != "QUIT" {
		t.Fatalf("encrypted quit = %q, %v", quit, err)
	}
	if err := serverEng.WriteLine("221 2.0.0 bye"); err != nil {
		t.Fatalf("encrypted reply: %v", err)
	}

	select {
	case res := <-clientDone:
		if res.err != nil {
			t.Fatalf("client: %v", res.err)
		}
		if res.line != "221 2.0.0 bye\r\n" {
			t.Fatalf("client got %q", res.line)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("client never finished")
	}

	// Tear the whole pipe down synchronously before the function returns so
	// no TLS/net background timer survives into package shutdown.
	_ = clientRaw.Close()
	_ = serverEng.Close()
}

// TestRealTLSPipelinedAttack verifies the boundary with real connections:
// plaintext bytes piggybacked on the STARTTLS write force the Engine to
// reject the upgrade before tls.Server ever runs, and the peer observes a
// closed connection.
func TestRealTLSPipelinedAttack(t *testing.T) {
	serverRaw, clientRaw := net.Pipe()

	serverEng, err := starttls.NewEngine(starttls.Config{Conn: serverRaw})
	if err != nil {
		t.Fatalf("server NewEngine: %v", err)
	}
	t.Cleanup(func() { _ = serverEng.Close() })

	clientErr := make(chan error, 1)
	go func() {
		_ = clientRaw.SetDeadline(time.Now().Add(5 * time.Second))
		_, werr := clientRaw.Write([]byte("STARTTLS\r\nMAIL FROM:<attacker@evil>\r\n"))
		if werr != nil {
			clientErr <- werr
			return
		}
		// After rejection the server closes; drain any buffered bytes and
		// keep reading until the closed-pipe error is observed.
		for {
			buf := make([]byte, 32)
			if _, rerr := clientRaw.Read(buf); rerr != nil {
				clientErr <- rerr
				return
			}
		}
	}()

	cmd, err := serverEng.ReadLine()
	if err != nil || cmd != "STARTTLS" {
		t.Fatalf("ReadLine = %q, %v", cmd, err)
	}
	if err := serverEng.WriteLine("220 Go ahead"); err != nil {
		t.Fatalf("go-ahead: %v", err)
	}

	upgradeRan := false
	err = serverEng.BeginUpgrade(context.Background(), func(context.Context, starttls.DuplexConn) (starttls.DuplexConn, error) {
		upgradeRan = true
		t.Fatal("tls.Server must not start when plaintext is pipelined")
		return nil, nil
	})
	if !errors.Is(err, starttls.ErrPipelined) {
		t.Fatalf("BeginUpgrade err = %v, want ErrPipelined", err)
	}
	if upgradeRan {
		t.Fatal("upgrade callback ran despite pipelining")
	}
	if serverEng.Phase() != starttls.Closed {
		t.Fatalf("phase = %s, want closed", serverEng.Phase())
	}

	select {
	case perr := <-clientErr:
		if perr == nil {
			t.Fatal("peer read after rejection must fail (connection closed)")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for peer")
	}
}
