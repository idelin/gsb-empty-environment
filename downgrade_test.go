package netsec

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"errors"
	"io"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	testCertOnce sync.Once
	testCert     tls.Certificate
	testCertErr  error
)

func certificate(t *testing.T) tls.Certificate {
	t.Helper()
	testCertOnce.Do(func() {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			testCertErr = err
			return
		}
		serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
		if err != nil {
			testCertErr = err
			return
		}
		template := &x509.Certificate{
			SerialNumber: serial,
			Subject:      pkix.Name{CommonName: "netsec.test"},
			DNSNames:     []string{"netsec.test"},
			NotBefore:    time.Now().Add(-time.Hour),
			NotAfter:     time.Now().Add(time.Hour),
			KeyUsage:     x509.KeyUsageDigitalSignature,
			ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		}
		der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
		if err != nil {
			testCertErr = err
			return
		}
		testCert = tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	})
	if testCertErr != nil {
		t.Fatalf("create certificate: %v", testCertErr)
	}
	return testCert
}

type recordingConn struct {
	net.Conn
	mu      sync.Mutex
	inbound []byte
}

func (c *recordingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.mu.Lock()
		c.inbound = append(c.inbound, p[:n]...)
		c.mu.Unlock()
	}
	return n, err
}

func (c *recordingConn) captured() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.inbound...)
}

func firstTLSRecord(t *testing.T, wire []byte) []byte {
	t.Helper()
	if len(wire) < 5 {
		t.Fatalf("captured wire data too short: %d", len(wire))
	}
	n := int(binary.BigEndian.Uint16(wire[3:5])) + 5
	if n > len(wire) {
		t.Fatalf("captured record truncated: need %d bytes, have %d", n, len(wire))
	}
	return wire[:n]
}

type handshakeResult struct {
	clientErr error
	serverErr error
	wire      []byte
}

func runHandshake(t *testing.T, serverCfg, clientCfg *tls.Config, capture bool) handshakeResult {
	t.Helper()
	serverRaw, clientRaw := net.Pipe()
	var rec *recordingConn
	if capture {
		rec = &recordingConn{Conn: clientRaw}
		clientRaw = rec
	}

	serverDone := make(chan error, 1)
	go func() {
		conn := Server(serverRaw, serverCfg)
		serverDone <- conn.Handshake()
	}()

	conn := Client(clientRaw, clientCfg)
	clientErr := conn.Handshake()
	serverErr := <-serverDone
	_ = serverRaw.Close()
	_ = clientRaw.Close()

	result := handshakeResult{clientErr: clientErr, serverErr: serverErr}
	if rec != nil {
		result.wire = rec.captured()
	}
	return result
}

func TestServerDowngradeSentinel(t *testing.T) {
	cert := certificate(t)
	cbcSuite := []uint16{tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA}

	tests := []struct {
		name         string
		serverMax    uint16
		serverMin    uint16
		clientMax    uint16
		clientMin    uint16
		cipherSuites []uint16
		wantSentinel string
	}{
		{
			name:         "TLS 1.3 server negotiates TLS 1.2",
			serverMax:    tls.VersionTLS13,
			serverMin:    tls.VersionTLS12,
			clientMax:    tls.VersionTLS12,
			clientMin:    tls.VersionTLS12,
			wantSentinel: TLS12DowngradeSentinel,
		},
		{
			name:         "TLS 1.3 server negotiates TLS 1.1",
			serverMax:    tls.VersionTLS13,
			serverMin:    tls.VersionTLS11,
			clientMax:    tls.VersionTLS11,
			clientMin:    tls.VersionTLS11,
			cipherSuites: cbcSuite,
			wantSentinel: TLS11DowngradeSentinel,
		},
		{
			name:         "TLS 1.2-only server writes no sentinel",
			serverMax:    tls.VersionTLS12,
			serverMin:    tls.VersionTLS12,
			clientMax:    tls.VersionTLS12,
			clientMin:    tls.VersionTLS12,
			wantSentinel: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			serverCfg := &tls.Config{
				Certificates: []tls.Certificate{cert},
				MinVersion:   tc.serverMin,
				MaxVersion:   tc.serverMax,
			}
			clientCfg := &tls.Config{
				InsecureSkipVerify: true,
				ServerName:         "netsec.test",
				MinVersion:         tc.clientMin,
				MaxVersion:         tc.clientMax,
				CipherSuites:       tc.cipherSuites,
			}
			result := runHandshake(t, serverCfg, clientCfg, true)
			if result.clientErr != nil || result.serverErr != nil {
				t.Fatalf("handshake failed: client=%v server=%v", result.clientErr, result.serverErr)
			}

			hello, err := parseServerHelloRecord(firstTLSRecord(t, result.wire))
			if err != nil {
				t.Fatalf("parse ServerHello: %v (wire=%x)", err, result.wire)
			}
			got := string(hello.random[len(hello.random)-8:])
			if tc.wantSentinel == "" {
				if got == TLS12DowngradeSentinel || got == TLS11DowngradeSentinel {
					t.Fatalf("TLS 1.2-only ServerHello unexpectedly has sentinel %q", got)
				}
			} else if got != tc.wantSentinel {
				t.Fatalf("ServerHello.random suffix = %q, want %q", got, tc.wantSentinel)
			}
		})
	}
}

func makeServerHelloRecord(legacyVersion uint16, random []byte) []byte {
	const hsLen = 2 + 32 + 1 + 2 + 1 + 2
	record := make([]byte, 9+hsLen)
	record[0] = recordTypeHandshake
	binary.BigEndian.PutUint16(record[1:3], 0x0303)
	binary.BigEndian.PutUint16(record[3:5], uint16(4+hsLen))
	record[5] = handshakeServerHello
	record[6] = 0
	record[7] = 0
	record[8] = hsLen
	p := 9
	binary.BigEndian.PutUint16(record[p:p+2], legacyVersion)
	p += 2
	copy(record[p:p+32], random)
	p += 32
	record[p] = 0 // empty session_id
	p++
	binary.BigEndian.PutUint16(record[p:p+2], tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256)
	p += 2
	record[p] = 0 // null compression
	p++
	binary.BigEndian.PutUint16(record[p:p+2], 0)
	return record
}

func TestClientRejectsServerDowngradeSentinel(t *testing.T) {
	for _, tc := range []struct {
		name     string
		version  uint16
		sentinel string
	}{
		{"TLS 1.2 sentinel", tls.VersionTLS12, TLS12DowngradeSentinel},
		{"TLS 1.1 sentinel", tls.VersionTLS11, TLS11DowngradeSentinel},
	} {
		t.Run(tc.name, func(t *testing.T) {
			serverRaw, clientRaw := net.Pipe()
			alertCh := make(chan []byte, 1)
			go func() {
				hello, err := readTLSRecord(serverRaw)
				if err != nil {
					alertCh <- nil
					return
				}
				_ = hello
				random := make([]byte, 32)
				copy(random[24:], tc.sentinel)
				if _, err := serverRaw.Write(makeServerHelloRecord(tc.version, random)); err != nil {
					alertCh <- nil
					return
				}
				alert, err := readTLSRecord(serverRaw)
				if err != nil {
					alertCh <- nil
					return
				}
				alertCh <- alert
			}()

			conn := Client(clientRaw, &tls.Config{
				InsecureSkipVerify: true,
				ServerName:         "netsec.test",
				MaxVersion:         tls.VersionTLS13,
			})
			err := conn.Handshake()
			if !errors.Is(err, ErrDowngradeDetected) {
				t.Fatalf("client error = %v, want ErrDowngradeDetected", err)
			}
			alert := <-alertCh
			if len(alert) < 7 || alert[0] != recordTypeAlert || alert[6] != AlertIllegalParameter {
				t.Fatalf("client alert = %x, want fatal illegal_parameter", alert)
			}
			_ = clientRaw.Close()
			_ = serverRaw.Close()
		})
	}
}

func makeClientHelloRecord(legacyVersion uint16, suites []uint16) []byte {
	const extensionsLen = 10 + 8 // supported_groups and signature_algorithms
	bodyLen := 2 + 32 + 1 + 2 + len(suites)*2 + 1 + 1 + 2 + extensionsLen
	record := make([]byte, 9+bodyLen)
	record[0] = recordTypeHandshake
	binary.BigEndian.PutUint16(record[1:3], 0x0301)
	binary.BigEndian.PutUint16(record[3:5], uint16(4+bodyLen))
	record[5] = handshakeClientHello
	record[6] = 0
	record[7] = 0
	record[8] = byte(bodyLen)
	p := 9
	binary.BigEndian.PutUint16(record[p:p+2], legacyVersion)
	p += 2
	p += 32
	record[p] = 0 // empty session_id
	p++
	binary.BigEndian.PutUint16(record[p:p+2], uint16(len(suites)*2))
	p += 2
	for _, suite := range suites {
		binary.BigEndian.PutUint16(record[p:p+2], suite)
		p += 2
	}
	record[p] = 1 // one compression method
	p++
	record[p] = 0 // null compression
	p++
	binary.BigEndian.PutUint16(record[p:p+2], extensionsLen)
	p += 2
	binary.BigEndian.PutUint16(record[p:p+2], 10) // supported_groups
	binary.BigEndian.PutUint16(record[p+2:p+4], 6)
	binary.BigEndian.PutUint16(record[p+4:p+6], 4)
	binary.BigEndian.PutUint16(record[p+6:p+8], uint16(tls.X25519))
	binary.BigEndian.PutUint16(record[p+8:p+10], uint16(tls.CurveP256))
	p += 10
	binary.BigEndian.PutUint16(record[p:p+2], 13) // signature_algorithms
	binary.BigEndian.PutUint16(record[p+2:p+4], 4)
	binary.BigEndian.PutUint16(record[p+4:p+6], 2)
	binary.BigEndian.PutUint16(record[p+6:p+8], 0x0403) // ecdsa_secp256r1_sha256
	return record
}

func TestServerRejectsInappropriateFallback(t *testing.T) {
	serverRaw, clientRaw := net.Pipe()
	serverDone := make(chan error, 1)
	go func() {
		conn := Server(serverRaw, &tls.Config{
			Certificates: []tls.Certificate{certificate(t)},
			MinVersion:   tls.VersionTLS10,
			MaxVersion:   tls.VersionTLS13,
		})
		serverDone <- conn.Handshake()
	}()

	hello := makeClientHelloRecord(tls.VersionTLS12, []uint16{
		tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
		TLSFallbackSCSV,
	})
	if _, err := clientRaw.Write(hello); err != nil {
		t.Fatalf("write ClientHello: %v", err)
	}
	alert, err := readTLSRecord(clientRaw)
	if err != nil {
		t.Fatalf("read alert: %v", err)
	}
	if alert[0] != recordTypeAlert || alert[6] != AlertInappropriateFallback {
		serverErr := <-serverDone
		t.Fatalf("server alert = %x, want inappropriate_fallback (ClientHello=%x serverErr=%v)", alert, hello, serverErr)
	}
	_ = clientRaw.Close()
	serverErr := <-serverDone
	if serverErr == nil || !strings.Contains(serverErr.Error(), "inappropriate protocol fallback") {
		t.Fatalf("server error = %v, want inappropriate protocol fallback", serverErr)
	}
	_ = serverRaw.Close()
}

func TestFirstClientHelloHasNoFallbackSCSV(t *testing.T) {
	serverRaw, clientRaw := net.Pipe()
	helloCh := make(chan []byte, 1)
	go func() {
		hello, err := readTLSRecord(serverRaw)
		if err != nil {
			helloCh <- nil
			return
		}
		helloCh <- hello
		_ = serverRaw.Close()
	}()

	conn := Client(clientRaw, &tls.Config{
		InsecureSkipVerify: true,
		ServerName:         "netsec.test",
		MaxVersion:         tls.VersionTLS13,
	})
	_ = conn.Handshake() // the capture server closes after reading ClientHello
	hello := <-helloCh
	parts, err := parseClientHelloRecord(hello)
	if err != nil {
		t.Fatalf("parse ClientHello: %v (record=%x)", err, hello)
	}
	if HasFallbackSCSV(parts.cipherSuites) {
		t.Fatalf("first ClientHello unexpectedly contains TLS_FALLBACK_SCSV")
	}
	_ = clientRaw.Close()
}

func TestAllowedFallbackSCSVHandshakeSucceeds(t *testing.T) {
	serverRaw, clientRaw := net.Pipe()
	serverCfg := &tls.Config{
		Certificates: []tls.Certificate{certificate(t)},
		MinVersion:   tls.VersionTLS12,
		MaxVersion:   tls.VersionTLS12,
	}
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- Server(serverRaw, serverCfg).Handshake()
	}()

	clientCfg := &tls.Config{
		InsecureSkipVerify: true,
		ServerName:         "netsec.test",
		MinVersion:         tls.VersionTLS12,
		MaxVersion:         tls.VersionTLS12,
	}
	conn := newClientTLSConn(clientRaw, clientCfg, true)
	if err := conn.Handshake(); err != nil {
		t.Fatalf("SCSV retry at server maximum failed: %v", err)
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("server handshake failed: %v", err)
	}
	_ = clientRaw.Close()
	_ = serverRaw.Close()
}

func TestFallbackRetryAddsSCSVOnlyAfterFailure(t *testing.T) {
	serverConns := make(chan net.Conn, 2)
	hellos := make(chan []byte, 2)
	serverErrs := make(chan error, 1)
	go func() {
		for i := 0; i < 2; i++ {
			conn := <-serverConns
			hello, err := readTLSRecord(conn)
			if err != nil {
				conn.Close()
				serverErrs <- err
				return
			}
			hellos <- hello
			if i == 0 {
				_ = sendTLSAlert(conn, 40) // handshake_failure
			}
			conn.Close()
		}
		serverErrs <- nil
	}()

	dial := func(context.Context, string, string) (net.Conn, error) {
		serverConn, clientConn := net.Pipe()
		serverConns <- serverConn
		return clientConn, nil
	}

	_, err := dialFallbackVersionsWithFunc(context.Background(), "pipe", "netsec.test:443", &tls.Config{
		InsecureSkipVerify: true,
		MaxVersion:         tls.VersionTLS13,
	}, []uint16{tls.VersionTLS13, tls.VersionTLS12}, dial)
	if err == nil {
		t.Fatal("fallback dial unexpectedly succeeded")
	}

	first := <-hellos
	second := <-hellos
	if err := <-serverErrs; err != nil {
		t.Fatalf("capture server: %v", err)
	}

	firstParts, err := parseClientHelloRecord(first)
	if err != nil {
		t.Fatalf("parse first ClientHello: %v", err)
	}
	if HasFallbackSCSV(firstParts.cipherSuites) {
		t.Fatalf("first ClientHello contains TLS_FALLBACK_SCSV")
	}
	if firstParts.legacyVersion != tls.VersionTLS12 {
		t.Fatalf("first ClientHello legacy_version = %#x, want TLS 1.2", firstParts.legacyVersion)
	}

	secondParts, err := parseClientHelloRecord(second)
	if err != nil {
		t.Fatalf("parse retry ClientHello: %v (second=%x)", err, second)
	}
	if !HasFallbackSCSV(secondParts.cipherSuites) {
		t.Fatalf("fallback ClientHello does not contain TLS_FALLBACK_SCSV (second=%x)", second)
	}
	if secondParts.legacyVersion != tls.VersionTLS12 {
		t.Fatalf("fallback ClientHello legacy_version = %#x, want TLS 1.2", secondParts.legacyVersion)
	}
}

func TestPolicyHelpers(t *testing.T) {
	if got := DowngradeSentinel(tls.VersionTLS13, tls.VersionTLS12); !bytes.Equal(got, []byte(TLS12DowngradeSentinel)) {
		t.Fatalf("TLS 1.2 sentinel = %x", got)
	}
	if got := DowngradeSentinel(tls.VersionTLS13, tls.VersionTLS11); !bytes.Equal(got, []byte(TLS11DowngradeSentinel)) {
		t.Fatalf("TLS 1.1 sentinel = %x", got)
	}
	if got := DowngradeSentinel(tls.VersionTLS12, tls.VersionTLS12); got != nil {
		t.Fatalf("TLS 1.2-only server sentinel = %x, want nil", got)
	}
	if err := CheckFallbackSCSV(tls.VersionTLS13, tls.VersionTLS12, []uint16{TLSFallbackSCSV}); !errors.Is(err, ErrInappropriateFallback) {
		t.Fatalf("SCSV check error = %v", err)
	}
	if err := CheckFallbackSCSV(tls.VersionTLS12, tls.VersionTLS12, []uint16{TLSFallbackSCSV}); err != nil {
		t.Fatalf("SCSV at server max should be legal, got %v", err)
	}
}

func TestApplyServerRandomSentinelPreservesRandomPrefix(t *testing.T) {
	random := bytes.Repeat([]byte{0xaa}, 32)
	if err := ApplyServerRandomSentinel(random, tls.VersionTLS13, tls.VersionTLS12); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(random[:24], bytes.Repeat([]byte{0xaa}, 24)) {
		t.Fatal("sentinel application modified random prefix")
	}
	if string(random[24:]) != TLS12DowngradeSentinel {
		t.Fatalf("sentinel suffix = %q", random[24:])
	}
	if err := CheckServerRandom(tls.VersionTLS13, tls.VersionTLS12, random); !errors.Is(err, ErrDowngradeDetected) {
		t.Fatalf("client check error = %v", err)
	}

	safe := bytes.Repeat([]byte{0xbb}, 32)
	if err := CheckServerRandom(tls.VersionTLS13, tls.VersionTLS12, safe); err != nil {
		t.Fatalf("safe random rejected: %v", err)
	}
}

func TestTLS11HandshakeDoesNotPanicOnShortRandom(t *testing.T) {
	if err := CheckServerRandom(tls.VersionTLS13, tls.VersionTLS11, []byte{1, 2, 3}); !errors.Is(err, ErrShortServerRandom) {
		t.Fatalf("short random error = %v", err)
	}
}

var _ io.Reader = (*recordingConn)(nil)
