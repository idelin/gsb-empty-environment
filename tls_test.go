package netsec

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
)

func TestCertificateMustMatchOriginHost(t *testing.T) {
	serverCert, leaf, roots := makeTestCertificate(t, "alt.example")
	rawLeaf := leaf.Raw

	if err := VerifyCertificateForOrigin("https://alt.example:8443", leaf); err != nil {
		t.Fatalf("certificate valid for alternate host was rejected: %v", err)
	}
	if err := VerifyCertificateForOrigin("https://origin.example", leaf); err == nil {
		t.Fatal("certificate valid only for alternate host was accepted for origin host")
	}
	if err := VerifyPeerCertificatesForOrigin("https://origin.example", [][]byte{rawLeaf}); err == nil {
		t.Fatal("VerifyPeerCertificatesForOrigin accepted an alternate-host-only certificate")
	}

	if err := handshakeWithOrigin("https://alt.example", serverCert, roots); err != nil {
		t.Fatalf("TLS handshake with a certificate valid for the dialed name failed: %v", err)
	}
	if err := handshakeWithOrigin("https://origin.example", serverCert, roots); err == nil {
		t.Fatal("TLS handshake succeeded although the certificate is only valid for the alternate host")
	}
}

func TestTLSConfigForOriginOverridesServerName(t *testing.T) {
	base := &tls.Config{ServerName: "alt.example", InsecureSkipVerify: true}
	config, err := TLSConfigForOrigin("https://origin.example:8443", base)
	if err != nil {
		t.Fatalf("TLSConfigForOrigin: %v", err)
	}
	if config.ServerName != "origin.example" {
		t.Errorf("ServerName = %q, want origin.example", config.ServerName)
	}
	if config.InsecureSkipVerify {
		t.Error("InsecureSkipVerify = true, want false")
	}
	if base.ServerName != "alt.example" || !base.InsecureSkipVerify {
		t.Error("TLSConfigForOrigin mutated its base config")
	}
}

func makeTestCertificate(t *testing.T, dnsName string) (tls.Certificate, *x509.Certificate, *x509.CertPool) {
	t.Helper()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "altsvc test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create CA certificate: %v", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatalf("parse CA certificate: %v", err)
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: dnsName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{dnsName},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create leaf certificate: %v", err)
	}
	leaf, err := x509.ParseCertificate(leafDER)
	if err != nil {
		t.Fatalf("parse leaf certificate: %v", err)
	}

	roots := x509.NewCertPool()
	roots.AddCert(caCert)
	return tls.Certificate{
		Certificate: [][]byte{leafDER, caDER},
		PrivateKey:  leafKey,
		Leaf:        leaf,
	}, leaf, roots
}

func handshakeWithOrigin(origin string, serverCert tls.Certificate, roots *x509.CertPool) error {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	deadline := time.Now().Add(5 * time.Second)
	serverConn.SetDeadline(deadline)
	clientConn.SetDeadline(deadline)

	server := tls.Server(serverConn, &tls.Config{Certificates: []tls.Certificate{serverCert}})
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		_ = server.Handshake()
	}()

	config, err := TLSConfigForOrigin(origin, &tls.Config{RootCAs: roots})
	if err != nil {
		clientConn.Close()
		return err
	}
	client := tls.Client(clientConn, config)
	err = client.Handshake()
	clientConn.Close()
	<-serverDone
	return err
}
