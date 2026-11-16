package netsec

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
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
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			testCertErr = err
			return
		}
		template := &x509.Certificate{
			SerialNumber:          big.NewInt(1),
			Subject:               pkix.Name{CommonName: "example.com"},
			DNSNames:              []string{"example.com"},
			NotBefore:             time.Now().Add(-time.Hour),
			NotAfter:              time.Now().Add(24 * time.Hour),
			KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
			ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			BasicConstraintsValid: true,
		}
		der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
		if err != nil {
			testCertErr = err
			return
		}
		certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
		keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
		testCert, testCertErr = tls.X509KeyPair(certPEM, keyPEM)
	})
	if testCertErr != nil {
		t.Fatalf("failed to create test certificate: %v", testCertErr)
	}
	return testCert
}

type captureConn struct {
	net.Conn
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *captureConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	c.buf.Write(p)
	c.mu.Unlock()
	return c.Conn.Write(p)
}

func (c *captureConn) bytes() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return bytes.Clone(c.buf.Bytes())
}

type handshakeResult struct {
	serverErr     error
	clientErr     error
	serverVersion uint16
	clientVersion uint16
	serverWire    []byte
	clientWire    []byte
}

func runHandshake(t *testing.T, serverConfig, clientConfig *tls.Config, opts ...ClientOption) handshakeResult {
	t.Helper()

	serverRaw, clientRaw := net.Pipe()
	serverCapture := &captureConn{Conn: serverRaw}
	clientCapture := &captureConn{Conn: clientRaw}
	server := Server(serverCapture, serverConfig)
	client := Client(clientCapture, clientConfig, opts...)

	closeBoth := func() {
		serverRaw.Close()
		clientRaw.Close()
	}
	defer closeBoth()

	serverErrCh := make(chan error, 1)
	clientErrCh := make(chan error, 1)
	go func() { serverErrCh <- server.Handshake() }()
	go func() { clientErrCh <- client.Handshake() }()

	result := handshakeResult{
		serverErr: waitHandshake(t, serverErrCh, closeBoth),
		clientErr: waitHandshake(t, clientErrCh, closeBoth),
	}
	if result.serverErr == nil {
		result.serverVersion = server.ConnectionState().Version
	}
	if result.clientErr == nil {
		result.clientVersion = client.ConnectionState().Version
	}
	result.serverWire = serverCapture.bytes()
	result.clientWire = clientCapture.bytes()
	return result
}

func waitHandshake(t *testing.T, ch <-chan error, closeBoth func()) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(10 * time.Second):
		closeBoth()
		select {
		case err := <-ch:
			t.Fatalf("TLS handshake timed out; peer returned %v", err)
		case <-time.After(time.Second):
			t.Fatal("TLS handshake timed out")
		}
		return nil
	}
}

func firstRecord(t *testing.T, wire []byte) (recordType uint8, payload []byte) {
	t.Helper()
	if len(wire) < 5 {
		t.Fatalf("short TLS wire data: %x", wire)
	}
	n := int(binary.BigEndian.Uint16(wire[3:5]))
	if len(wire) < 5+n {
		t.Fatalf("truncated TLS record: have %d bytes, want %d", len(wire), 5+n)
	}
	return wire[0], wire[5 : 5+n]
}

func clientHelloFromWire(t *testing.T, wire []byte) *clientHelloInfo {
	t.Helper()
	recordType, payload := firstRecord(t, wire)
	if recordType != recordTypeHandshake {
		t.Fatalf("first record type = %d, want handshake", recordType)
	}
	if len(payload) < 4 || payload[0] != handshakeTypeClientHello {
		t.Fatalf("first handshake message is not a ClientHello: %x", payload)
	}
	info, err := parseClientHello(payload)
	if err != nil {
		t.Fatalf("failed to parse ClientHello: %v", err)
	}
	return info
}

func serverHelloFromWire(t *testing.T, wire []byte) *serverHelloInfo {
	t.Helper()
	recordType, payload := firstRecord(t, wire)
	if recordType != recordTypeHandshake {
		t.Fatalf("first record type = %d, want handshake", recordType)
	}
	if len(payload) < 4 || payload[0] != handshakeTypeServerHello {
		t.Fatalf("first handshake message is not a ServerHello: %x", payload)
	}
	info, err := parseServerHello(payload)
	if err != nil {
		t.Fatalf("failed to parse ServerHello: %v", err)
	}
	return info
}

func alertFromWire(t *testing.T, wire []byte) uint8 {
	t.Helper()
	recordType, payload := firstRecord(t, wire)
	if recordType != recordTypeAlert {
		t.Fatalf("first record type = %d, want alert", recordType)
	}
	if len(payload) < 2 {
		t.Fatalf("short alert payload: %x", payload)
	}
	return payload[1]
}

func TestServerWritesTLS12Sentinel(t *testing.T) {
	result := runHandshake(t,
		&tls.Config{
			Certificates: []tls.Certificate{certificate(t)},
			MinVersion:   tls.VersionTLS12,
			MaxVersion:   tls.VersionTLS13,
		},
		&tls.Config{
			InsecureSkipVerify: true,
			ServerName:         "example.com",
			MinVersion:         tls.VersionTLS12,
			MaxVersion:         tls.VersionTLS12,
		},
	)
	if result.serverErr != nil || result.clientErr != nil {
		t.Fatalf("handshake failed: server=%v client=%v", result.serverErr, result.clientErr)
	}
	if result.clientVersion != tls.VersionTLS12 {
		t.Fatalf("negotiated version = %#x, want TLS 1.2", result.clientVersion)
	}
	info := serverHelloFromWire(t, result.serverWire)
	if !bytes.Equal(info.random[24:], SentinelTLS12) {
		t.Fatalf("ServerHello.random suffix = %x, want %x", info.random[24:], SentinelTLS12)
	}
}

func TestServerWritesTLS11Sentinel(t *testing.T) {
	suite := []uint16{tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA}
	result := runHandshake(t,
		&tls.Config{
			Certificates: []tls.Certificate{certificate(t)},
			MinVersion:   tls.VersionTLS10,
			MaxVersion:   tls.VersionTLS13,
			CipherSuites: suite,
		},
		&tls.Config{
			InsecureSkipVerify: true,
			ServerName:         "example.com",
			MinVersion:         tls.VersionTLS10,
			MaxVersion:         tls.VersionTLS11,
			CipherSuites:       suite,
		},
	)
	if result.serverErr != nil || result.clientErr != nil {
		t.Fatalf("handshake failed: server=%v client=%v", result.serverErr, result.clientErr)
	}
	if result.clientVersion != tls.VersionTLS11 {
		t.Fatalf("negotiated version = %#x, want TLS 1.1", result.clientVersion)
	}
	info := serverHelloFromWire(t, result.serverWire)
	if !bytes.Equal(info.random[24:], SentinelTLS11) {
		t.Fatalf("ServerHello.random suffix = %x, want %x", info.random[24:], SentinelTLS11)
	}
}

func TestTLS12ServerDoesNotWriteSentinelWhenNegotiatingTLS11(t *testing.T) {
	suite := []uint16{tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA}
	result := runHandshake(t,
		&tls.Config{
			Certificates: []tls.Certificate{certificate(t)},
			MinVersion:   tls.VersionTLS10,
			MaxVersion:   tls.VersionTLS12,
			CipherSuites: suite,
		},
		&tls.Config{
			InsecureSkipVerify: true,
			ServerName:         "example.com",
			MinVersion:         tls.VersionTLS10,
			MaxVersion:         tls.VersionTLS11,
			CipherSuites:       suite,
		},
	)
	if result.serverErr != nil || result.clientErr != nil {
		t.Fatalf("handshake failed: server=%v client=%v", result.serverErr, result.clientErr)
	}
	if result.clientVersion != tls.VersionTLS11 {
		t.Fatalf("negotiated version = %#x, want TLS 1.1", result.clientVersion)
	}
	info := serverHelloFromWire(t, result.serverWire)
	if bytes.Equal(info.random[24:], SentinelTLS12) || bytes.Equal(info.random[24:], SentinelTLS11) {
		t.Fatalf("TLS 1.2-only server wrote a downgrade sentinel: %x", info.random[24:])
	}
}

func TestSelectedTLS12ConfigDoesNotWriteSentinel(t *testing.T) {
	suite := []uint16{tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA}
	base := &tls.Config{
		Certificates: []tls.Certificate{certificate(t)},
		MinVersion:   tls.VersionTLS10,
		MaxVersion:   tls.VersionTLS13,
		CipherSuites: suite,
	}
	base.GetConfigForClient = func(*tls.ClientHelloInfo) (*tls.Config, error) {
		cfg := base.Clone()
		cfg.GetConfigForClient = nil
		cfg.MaxVersion = tls.VersionTLS12
		return cfg, nil
	}

	result := runHandshake(t, base,
		&tls.Config{
			InsecureSkipVerify: true,
			ServerName:         "example.com",
			MinVersion:         tls.VersionTLS10,
			MaxVersion:         tls.VersionTLS11,
			CipherSuites:       suite,
		},
	)
	if result.serverErr != nil || result.clientErr != nil {
		t.Fatalf("handshake failed: server=%v client=%v", result.serverErr, result.clientErr)
	}
	if result.clientVersion != tls.VersionTLS11 {
		t.Fatalf("negotiated version = %#x, want TLS 1.1", result.clientVersion)
	}
	info := serverHelloFromWire(t, result.serverWire)
	if bytes.Equal(info.random[24:], SentinelTLS12) || bytes.Equal(info.random[24:], SentinelTLS11) {
		t.Fatalf("TLS 1.2-only selected config wrote a downgrade sentinel: %x", info.random[24:])
	}
}

func TestServerRejectsFallbackSCSV(t *testing.T) {
	result := runHandshake(t,
		&tls.Config{
			Certificates: []tls.Certificate{certificate(t)},
			MinVersion:   tls.VersionTLS12,
			MaxVersion:   tls.VersionTLS13,
		},
		&tls.Config{
			InsecureSkipVerify: true,
			ServerName:         "example.com",
			MinVersion:         tls.VersionTLS12,
			MaxVersion:         tls.VersionTLS12,
		},
		WithFallback(),
	)
	if result.serverErr == nil || result.clientErr == nil {
		t.Fatalf("fallback handshake unexpectedly succeeded: server=%v client=%v", result.serverErr, result.clientErr)
	}
	var alertErr *AlertError
	if !errors.As(result.serverErr, &alertErr) || alertErr.Description != AlertInappropriateFallback {
		t.Fatalf("server error = %v, want inappropriate_fallback alert", result.serverErr)
	}
	if !strings.Contains(result.clientErr.Error(), "inappropriate fallback") {
		t.Fatalf("client error = %v, want inappropriate fallback", result.clientErr)
	}
	info := clientHelloFromWire(t, result.clientWire)
	if !HasFallbackSCSV(info.cipherSuites) {
		t.Fatalf("fallback ClientHello did not contain TLS_FALLBACK_SCSV: %x", info.cipherSuites)
	}
	if got := alertFromWire(t, result.serverWire); got != AlertInappropriateFallback {
		t.Fatalf("alert description = %d, want %d", got, AlertInappropriateFallback)
	}
}

func TestAcceptedFallbackSCSVHandshake(t *testing.T) {
	suite := []uint16{tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256}
	result := runHandshake(t,
		&tls.Config{
			Certificates: []tls.Certificate{certificate(t)},
			MinVersion:   tls.VersionTLS12,
			MaxVersion:   tls.VersionTLS12,
			CipherSuites: suite,
		},
		&tls.Config{
			InsecureSkipVerify: true,
			ServerName:         "example.com",
			MinVersion:         tls.VersionTLS12,
			MaxVersion:         tls.VersionTLS12,
			CipherSuites:       suite,
		},
		WithFallback(),
	)
	if result.serverErr != nil || result.clientErr != nil {
		t.Fatalf("fallback handshake failed: server=%v client=%v", result.serverErr, result.clientErr)
	}
	if result.clientVersion != tls.VersionTLS12 {
		t.Fatalf("negotiated version = %#x, want TLS 1.2", result.clientVersion)
	}
	info := clientHelloFromWire(t, result.clientWire)
	if !HasFallbackSCSV(info.cipherSuites) {
		t.Fatalf("fallback ClientHello did not contain TLS_FALLBACK_SCSV: %x", info.cipherSuites)
	}
}

func TestFirstHandshakeOmitsFallbackSCSV(t *testing.T) {
	cases := []struct {
		name   string
		suites []uint16
	}{
		{
			name: "with-real-suite",
			suites: []uint16{
				tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
				TLS_FALLBACK_SCSV,
			},
		},
		{
			name:   "only-scsv",
			suites: []uint16{TLS_FALLBACK_SCSV},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := runHandshake(t,
				&tls.Config{
					Certificates: []tls.Certificate{certificate(t)},
					MinVersion:   tls.VersionTLS12,
					MaxVersion:   tls.VersionTLS13,
				},
				&tls.Config{
					InsecureSkipVerify: true,
					ServerName:         "example.com",
					MinVersion:         tls.VersionTLS12,
					MaxVersion:         tls.VersionTLS12,
					CipherSuites:       tc.suites,
				},
			)
			if result.serverErr != nil || result.clientErr != nil {
				t.Fatalf("handshake failed: server=%v client=%v", result.serverErr, result.clientErr)
			}
			info := clientHelloFromWire(t, result.clientWire)
			if HasFallbackSCSV(info.cipherSuites) {
				t.Fatalf("initial ClientHello contained TLS_FALLBACK_SCSV: %x", info.cipherSuites)
			}
		})
	}
}

func TestLegalTLS12OnlyHandshake(t *testing.T) {
	result := runHandshake(t,
		&tls.Config{
			Certificates: []tls.Certificate{certificate(t)},
			MinVersion:   tls.VersionTLS12,
			MaxVersion:   tls.VersionTLS12,
		},
		&tls.Config{
			InsecureSkipVerify: true,
			ServerName:         "example.com",
			MinVersion:         tls.VersionTLS12,
			MaxVersion:         tls.VersionTLS13,
		},
	)
	if result.serverErr != nil || result.clientErr != nil {
		t.Fatalf("handshake failed: server=%v client=%v", result.serverErr, result.clientErr)
	}
	if result.clientVersion != tls.VersionTLS12 {
		t.Fatalf("negotiated version = %#x, want TLS 1.2", result.clientVersion)
	}
	info := serverHelloFromWire(t, result.serverWire)
	if bytes.Equal(info.random[24:], SentinelTLS12) || bytes.Equal(info.random[24:], SentinelTLS11) {
		t.Fatalf("TLS 1.2-only server wrote a downgrade sentinel: %x", info.random[24:])
	}
}

func TestLegalTLS13Handshake(t *testing.T) {
	result := runHandshake(t,
		&tls.Config{
			Certificates: []tls.Certificate{certificate(t)},
			MinVersion:   tls.VersionTLS13,
			MaxVersion:   tls.VersionTLS13,
		},
		&tls.Config{
			InsecureSkipVerify: true,
			ServerName:         "example.com",
			MinVersion:         tls.VersionTLS13,
			MaxVersion:         tls.VersionTLS13,
		},
	)
	if result.serverErr != nil || result.clientErr != nil {
		t.Fatalf("handshake failed: server=%v client=%v", result.serverErr, result.clientErr)
	}
	if result.clientVersion != tls.VersionTLS13 {
		t.Fatalf("negotiated version = %#x, want TLS 1.3", result.clientVersion)
	}
	info := serverHelloFromWire(t, result.serverWire)
	if bytes.Equal(info.random[24:], SentinelTLS12) || bytes.Equal(info.random[24:], SentinelTLS11) {
		t.Fatalf("TLS 1.3 ServerHello contained a downgrade sentinel: %x", info.random[24:])
	}
}

func TestServerRejectsSCSVFromTLS13Client(t *testing.T) {
	result := runHandshake(t,
		&tls.Config{
			Certificates: []tls.Certificate{certificate(t)},
			MinVersion:   tls.VersionTLS13,
			MaxVersion:   tls.VersionTLS13,
		},
		&tls.Config{
			InsecureSkipVerify: true,
			ServerName:         "example.com",
			MinVersion:         tls.VersionTLS13,
			MaxVersion:         tls.VersionTLS13,
		},
		WithFallback(),
	)
	if result.serverErr == nil || result.clientErr == nil {
		t.Fatalf("SCSV handshake unexpectedly succeeded: server=%v client=%v", result.serverErr, result.clientErr)
	}
	var alertErr *AlertError
	if !errors.As(result.serverErr, &alertErr) || alertErr.Description != AlertInappropriateFallback {
		t.Fatalf("server error = %v, want inappropriate_fallback alert", result.serverErr)
	}
	if got := alertFromWire(t, result.serverWire); got != AlertInappropriateFallback {
		t.Fatalf("alert description = %d, want %d", got, AlertInappropriateFallback)
	}
	info := clientHelloFromWire(t, result.clientWire)
	if !HasFallbackSCSV(info.cipherSuites) {
		t.Fatalf("ClientHello did not contain TLS_FALLBACK_SCSV: %x", info.cipherSuites)
	}
}

func TestClientRejectsDowngradeSentinels(t *testing.T) {
	for _, sentinel := range [][]byte{SentinelTLS12, SentinelTLS11} {
		sentinel := bytes.Clone(sentinel)
		name := fmt.Sprintf("%x", sentinel)
		t.Run(name, func(t *testing.T) {
			clientErr, alert := fakeServerHello(t, sentinel)
			var alertErr *AlertError
			if !errors.As(clientErr, &alertErr) || alertErr.Description != AlertIllegalParameter {
				t.Fatalf("client error = %v, want illegal_parameter alert", clientErr)
			}
			if !errors.Is(clientErr, ErrDowngradeDetected) {
				t.Fatalf("client error = %v, want ErrDowngradeDetected", clientErr)
			}
			if alert != AlertIllegalParameter {
				t.Fatalf("client sent alert %d, want %d", alert, AlertIllegalParameter)
			}
		})
	}
}

func fakeServerHello(t *testing.T, sentinel []byte) (error, uint8) {
	t.Helper()

	serverRaw, clientRaw := net.Pipe()
	defer serverRaw.Close()
	defer clientRaw.Close()

	type serverResult struct {
		err   error
		alert uint8
	}
	resultCh := make(chan serverResult, 1)
	go func() {
		if _, _, _, err := readTLSRecord(serverRaw); err != nil {
			resultCh <- serverResult{err: err}
			return
		}
		if err := writeAll(serverRaw, marshalServerHello(tls.VersionTLS12, sentinel)); err != nil {
			resultCh <- serverResult{err: err}
			return
		}
		recordType, payload, err := readTLSRecordPayload(serverRaw)
		if err != nil {
			resultCh <- serverResult{err: err}
			return
		}
		if recordType != recordTypeAlert || len(payload) < 2 {
			resultCh <- serverResult{err: fmt.Errorf("unexpected client response: type=%d payload=%x", recordType, payload)}
			return
		}
		resultCh <- serverResult{alert: payload[1]}
	}()

	client := Client(clientRaw, &tls.Config{
		InsecureSkipVerify: true,
		ServerName:         "example.com",
		MinVersion:         tls.VersionTLS13,
		MaxVersion:         tls.VersionTLS13,
	})
	clientErr := client.Handshake()

	select {
	case result := <-resultCh:
		if result.err != nil {
			t.Fatalf("fake server failed: %v", result.err)
		}
		return clientErr, result.alert
	case <-time.After(10 * time.Second):
		t.Fatal("fake server timed out")
		return nil, 0
	}
}

func readTLSRecordPayload(r net.Conn) (uint8, []byte, error) {
	var header [5]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return 0, nil, err
	}
	n := int(binary.BigEndian.Uint16(header[3:5]))
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return header[0], payload, nil
}

func marshalServerHello(version uint16, sentinel []byte) []byte {
	random := make([]byte, 32)
	copy(random[24:], sentinel)

	body := make([]byte, 0, 2+32+1+2+1+2)
	var versionBytes [2]byte
	binary.BigEndian.PutUint16(versionBytes[:], version)
	body = append(body, versionBytes[:]...)
	body = append(body, random...)
	body = append(body, 0) // session ID
	body = append(body, 0xc0, 0x2f, 0)
	body = append(body, 0, 0) // extensions

	hello := []byte{
		handshakeTypeServerHello,
		byte(len(body) >> 16),
		byte(len(body) >> 8),
		byte(len(body)),
	}
	hello = append(hello, body...)

	record := []byte{recordTypeHandshake, 0x03, 0x03, 0, 0}
	binary.BigEndian.PutUint16(record[3:5], uint16(len(hello)))
	return append(record, hello...)
}
