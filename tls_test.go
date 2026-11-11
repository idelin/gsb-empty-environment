package ocspcheck

import (
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"math/big"
	"net"
	"testing"
	"time"
)

// TestRSASignatures proves the RSA verification branches (PKCS#1v1.5 and
// PSS) using an RSA issuing CA.
func TestRSASignatures(t *testing.T) {
	p := newTestPKI(fixedNow)

	rsaLeafKey := mustECDSAKey()
	rsaLeaf := p.mustLeaf(p.rsaCACert, p.rsaCAKey, rsaLeafKey.Public(), "rsa.example.test", 3001)

	base := func() responseInput {
		return responseInput{
			issuer:     p.rsaCACert,
			subject:    rsaLeaf,
			signerCert: p.rsaCACert,
			signerKey:  p.rsaCAKey,
			hashOID:    oidHashSHA1,
			hashFunc:   crypto.SHA1,
			sigOID:     oidSigSHA256WithRSA,
			thisUpdate: fixedNow.Add(-time.Minute),
			nextUpdate: fixedNow.Add(time.Hour),
			producedAt: fixedNow.Add(-time.Minute),
			embedCerts: nil,
		}
	}

	t.Run("RSA PKCS1v15 CA signature accepted", func(t *testing.T) {
		der := buildOCSPResponse(base())
		cs := tls.ConnectionState{
			PeerCertificates: []*x509.Certificate{rsaLeaf, p.rsaCACert},
			VerifiedChains:   [][]*x509.Certificate{{rsaLeaf, p.rsaCACert}},
			OCSPResponse:     der,
		}
		cfg := Config{}.withClock(func() time.Time { return fixedNow })
		if err := cfg.checkAt(cs, fixedNow); err != nil {
			t.Fatalf("expected success, got %v", err)
		}
	})

	t.Run("RSA-PSS CA signature accepted", func(t *testing.T) {
		in := base()
		in.sigOID = oidSigRSAPSS
		in.pss = true
		der := buildOCSPResponse(in)
		cs := tls.ConnectionState{
			PeerCertificates: []*x509.Certificate{rsaLeaf, p.rsaCACert},
			VerifiedChains:   [][]*x509.Certificate{{rsaLeaf, p.rsaCACert}},
			OCSPResponse:     der,
		}
		cfg := Config{}.withClock(func() time.Time { return fixedNow })
		if err := cfg.checkAt(cs, fixedNow); err != nil {
			t.Fatalf("expected success, got %v", err)
		}
	})

	t.Run("RSA response with wrong serial rejected", func(t *testing.T) {
		in := base()
		in.wrongSerial = big.NewInt(424242)
		der := buildOCSPResponse(in)
		cs := tls.ConnectionState{
			PeerCertificates: []*x509.Certificate{rsaLeaf, p.rsaCACert},
			OCSPResponse:     der,
		}
		cfg := Config{}.withClock(func() time.Time { return fixedNow })
		if err := cfg.checkAt(cs, fixedNow); err == nil {
			t.Fatal("expected serial mismatch error, got nil")
		}
	})
}

// TestTLSHandshakeIntegration runs a real in-process TLS handshake with the
// checker installed at tls.Config.VerifyConnection, proving the component is
// wire-compatible with crypto/tls staple handling.
func TestTLSHandshakeIntegration(t *testing.T) {
	p := newTestPKI(fixedNow)

	goodOCSP := buildOCSPResponse(func() responseInput {
		in := defaultResponseInput(p, p.caCert, p.caKey)
		in.embedCerts = nil
		return in
	}())

	revokedOCSP := buildOCSPResponse(func() responseInput {
		in := defaultResponseInput(p, p.caCert, p.caKey)
		in.embedCerts = nil
		in.status = statusRevoked
		return in
	}())

	serverCert := tls.Certificate{
		Certificate: [][]byte{p.leafCert.Raw, p.caCert.Raw},
		PrivateKey:  p.leafKey,
		Leaf:        p.leafCert,
	}

	roots := x509.NewCertPool()
	roots.AddCert(p.caCert)

	// runHandshake performs a full TLS handshake over an in-memory pipe. The
	// server's tls.Certificate carries the given staple (nil = none).
	runHandshake := func(t *testing.T, clientChecker *Config, staple []byte) error {
		t.Helper()
		// Build a fresh certificate copy per handshake so subtests never leak
		// a staple configured by an earlier case.
		cert := tls.Certificate{
			Certificate: serverCert.Certificate,
			PrivateKey:  serverCert.PrivateKey,
			Leaf:        serverCert.Leaf,
		}
		serverTLSConfig := &tls.Config{
			Certificates: []tls.Certificate{cert},
		}
		if staple != nil {
			serverTLSConfig.Certificates[0].OCSPStaple = staple
		}

		clientConn, serverConn := net.Pipe()
		serverTLSConn := tls.Server(serverConn, serverTLSConfig)

		clientTLSConfig := &tls.Config{
			RootCAs:            roots,
			InsecureSkipVerify: false,
			ServerName:         "example.test",
		}
		if clientChecker != nil {
			clientTLSConfig.VerifyConnection = clientChecker.VerifyConnection
		}
		clientTLSConn := tls.Client(clientConn, clientTLSConfig)

		serverErr := make(chan error, 1)
		go func() {
			serverErr <- serverTLSConn.Handshake()
		}()
		clientErr := clientTLSConn.Handshake()

		_ = clientTLSConn.Close()
		_ = serverTLSConn.Close()
		if serverErr := <-serverErr; serverErr != nil && clientErr == nil {
			return serverErr
		}
		return clientErr
	}

	t.Run("handshake succeeds with valid staple", func(t *testing.T) {
		checker := Config{}.withClock(func() time.Time { return fixedNow })
		if err := runHandshake(t, &checker, goodOCSP); err != nil {
			t.Fatalf("expected successful handshake, got %v", err)
		}
	})

	t.Run("handshake fails with revoked staple", func(t *testing.T) {
		checker := Config{}.withClock(func() time.Time { return fixedNow })
		if err := runHandshake(t, &checker, revokedOCSP); err == nil {
			t.Fatal("expected handshake to fail on revoked staple")
		}
	})

	t.Run("handshake fails without staple under strict policy", func(t *testing.T) {
		checker := Config{}.withClock(func() time.Time { return fixedNow })
		if err := runHandshake(t, &checker, nil); err == nil {
			t.Fatal("expected handshake to fail when staple missing")
		}
	})

	t.Run("handshake succeeds without staple under lenient policy", func(t *testing.T) {
		checker := Config{MissingPolicy: MissingAllowed}.withClock(func() time.Time { return fixedNow })
		if err := runHandshake(t, &checker, nil); err != nil {
			t.Fatalf("expected successful handshake under lenient policy, got %v", err)
		}
	})
}
