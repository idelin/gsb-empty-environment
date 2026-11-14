package netsec

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const (
	proxySAN    = "front-proxy"
	proxyAddr   = "10.1.2.3:8443"
	proxyCIDR   = "10.0.0.0/8"
	testCluster = "cluster-a"
)

type testCA struct {
	cert *x509.Certificate
	key  *rsa.PrivateKey
	pool *x509.CertPool
}

func newTestCA(t *testing.T, commonName string) *testCA {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create CA cert: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse CA cert: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &testCA{cert: cert, key: key, pool: pool}
}

func issueClientCert(t *testing.T, ca *testCA, serial int64, dnsNames ...string) *x509.Certificate {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate client key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: "proxy-client"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		DNSNames:     dnsNames,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatalf("create client cert: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse client cert: %v", err)
	}
	return cert
}

func newTestAuthenticator(t *testing.T, ca *testCA) *Authenticator {
	t.Helper()
	a, err := NewAuthenticator(Config{
		ClusterID:        testCluster,
		ProxyCIDRs:       []string{proxyCIDR},
		TrustedProxySANs: []string{proxySAN},
		ClientCAs:        ca.pool,
	})
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	return a
}

// newRequest builds a request as seen by the backend: remoteAddr is the peer
// address and leaf, when non-nil, is the mTLS client certificate.
func newRequest(remoteAddr string, leaf *x509.Certificate) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "https://aggregator.example/apis", nil)
	req.RemoteAddr = remoteAddr
	if leaf != nil {
		req.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{leaf}}
	} else {
		req.TLS = nil
	}
	return req
}

func setIdentityHeaders(req *http.Request, user string) {
	req.Header.Set("X-Remote-User", user)
	req.Header.Set("Audit-ID", "audit-0001")
}

func TestDirectClientForgedHeaderRejected(t *testing.T) {
	ca := newTestCA(t, "root-ca")
	a := newTestAuthenticator(t, ca)

	req := newRequest("203.0.113.9:5555", nil)
	setIdentityHeaders(req, "admin")
	if _, err := a.Authenticate(req); !errors.Is(err, ErrUntrustedProxyAddr) {
		t.Fatalf("direct client: got %v, want ErrUntrustedProxyAddr", err)
	}

	// A direct client on a proxy address but without a client certificate
	// is rejected as well.
	req = newRequest(proxyAddr, nil)
	setIdentityHeaders(req, "admin")
	if _, err := a.Authenticate(req); !errors.Is(err, ErrNoClientCert) {
		t.Fatalf("no client cert: got %v, want ErrNoClientCert", err)
	}

	// Through the middleware, the forged headers are stripped and the
	// spoofing attempt is rejected with 403.
	var downstreamSawHeader bool
	handler := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downstreamSawHeader = r.Header.Get("X-Remote-User") != ""
	}))
	req = newRequest("203.0.113.9:5555", nil)
	setIdentityHeaders(req, "admin")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("middleware status = %d, want 403", rec.Code)
	}
	if downstreamSawHeader {
		t.Fatal("forged identity header reached the backend")
	}
}

func TestPlainDirectClientPassesWithoutIdentity(t *testing.T) {
	ca := newTestCA(t, "root-ca")
	a := newTestAuthenticator(t, ca)

	var sawIdentity bool
	handler := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, sawIdentity = IdentityFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))
	req := newRequest("203.0.113.9:5555", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if sawIdentity {
		t.Fatal("direct client unexpectedly got an identity")
	}
}

func TestDuplicateHeaderRejected(t *testing.T) {
	ca := newTestCA(t, "root-ca")
	a := newTestAuthenticator(t, ca)
	cert := issueClientCert(t, ca, 100, proxySAN)

	req := newRequest(proxyAddr, cert)
	req.Header["X-Remote-User"] = []string{"alice", "mallory"}
	req.Header.Set("Audit-ID", "audit-0001")
	if _, err := a.Authenticate(req); !errors.Is(err, ErrDuplicateHeader) {
		t.Fatalf("got %v, want ErrDuplicateHeader", err)
	}
}

func TestCaseVariantHeaderRejected(t *testing.T) {
	ca := newTestCA(t, "root-ca")
	a := newTestAuthenticator(t, ca)
	cert := issueClientCert(t, ca, 100, proxySAN)

	req := newRequest(proxyAddr, cert)
	req.Header["x-remote-user"] = []string{"alice"}
	req.Header.Set("Audit-ID", "audit-0001")
	if _, err := a.Authenticate(req); !errors.Is(err, ErrNonCanonicalHeader) {
		t.Fatalf("got %v, want ErrNonCanonicalHeader", err)
	}
}

func TestInvalidValueRejected(t *testing.T) {
	ca := newTestCA(t, "root-ca")
	a := newTestAuthenticator(t, ca)
	cert := issueClientCert(t, ca, 100, proxySAN)

	req := newRequest(proxyAddr, cert)
	setIdentityHeaders(req, "evil user")
	if _, err := a.Authenticate(req); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("got %v, want ErrInvalidValue", err)
	}
}

func TestUntrustedProxyCertRejected(t *testing.T) {
	ca := newTestCA(t, "root-ca")
	a := newTestAuthenticator(t, ca)

	// Certificate from an unknown CA.
	otherCA := newTestCA(t, "rogue-ca")
	rogueCert := issueClientCert(t, otherCA, 100, proxySAN)
	req := newRequest(proxyAddr, rogueCert)
	setIdentityHeaders(req, "alice")
	if _, err := a.Authenticate(req); !errors.Is(err, ErrUntrustedProxyCert) {
		t.Fatalf("rogue CA: got %v, want ErrUntrustedProxyCert", err)
	}

	// Certificate from the trusted CA but with an untrusted SAN.
	wrongSAN := issueClientCert(t, ca, 100, "not-the-proxy")
	req = newRequest(proxyAddr, wrongSAN)
	setIdentityHeaders(req, "alice")
	if _, err := a.Authenticate(req); !errors.Is(err, ErrUntrustedProxyCert) {
		t.Fatalf("wrong SAN: got %v, want ErrUntrustedProxyCert", err)
	}
}

func TestLegitimateFrontProxyAccepted(t *testing.T) {
	ca := newTestCA(t, "root-ca")
	a := newTestAuthenticator(t, ca)
	cert := issueClientCert(t, ca, 100, proxySAN)

	req := newRequest(proxyAddr, cert)
	setIdentityHeaders(req, "system:serviceaccount:web:frontend")
	req.Header.Set("X-Remote-Group", "system:authenticated,devs")

	id, err := a.Authenticate(req)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if id.User != "system:serviceaccount:web:frontend" {
		t.Fatalf("user = %q", id.User)
	}
	if len(id.Groups) != 2 || id.Groups[0] != "system:authenticated" || id.Groups[1] != "devs" {
		t.Fatalf("groups = %v", id.Groups)
	}
	if id.ClusterID != testCluster {
		t.Fatalf("cluster = %q", id.ClusterID)
	}
	if id.ProxyInstance != proxySAN {
		t.Fatalf("proxy instance = %q", id.ProxyInstance)
	}
	if id.CertGeneration != big.NewInt(100).Text(16) {
		t.Fatalf("cert generation = %q", id.CertGeneration)
	}
	if id.AuditID != "audit-0001" {
		t.Fatalf("audit ID = %q", id.AuditID)
	}

	// End to end through the middleware the identity reaches the handler.
	var got *Identity
	handler := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = IdentityFromContext(r.Context())
	}))
	req = newRequest(proxyAddr, cert)
	setIdentityHeaders(req, "alice")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got == nil || got.User != "alice" {
		t.Fatalf("middleware identity = %+v", got)
	}
}

func TestCertRotationInvalidatesOldGeneration(t *testing.T) {
	ca := newTestCA(t, "root-ca")
	a := newTestAuthenticator(t, ca)
	oldCert := issueClientCert(t, ca, 100, proxySAN)
	newCert := issueClientCert(t, ca, 200, proxySAN)

	// Proxy reconnects with a rotated (newer) certificate: accepted.
	req := newRequest(proxyAddr, newCert)
	setIdentityHeaders(req, "alice")
	id, err := a.Authenticate(req)
	if err != nil {
		t.Fatalf("rotated cert: %v", err)
	}
	if id.CertGeneration != big.NewInt(200).Text(16) {
		t.Fatalf("generation = %q", id.CertGeneration)
	}

	// The old connection/certificate generation is now invalid.
	req = newRequest(proxyAddr, oldCert)
	setIdentityHeaders(req, "alice")
	if _, err := a.Authenticate(req); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("old generation: got %v, want ErrStaleGeneration", err)
	}

	// The current generation keeps working.
	req = newRequest(proxyAddr, newCert)
	setIdentityHeaders(req, "alice")
	if _, err := a.Authenticate(req); err != nil {
		t.Fatalf("current generation: %v", err)
	}
}

func TestStripIdentityHeadersRemovesAllCaseVariants(t *testing.T) {
	h := http.Header{
		"X-Remote-User":  {"alice"},
		"x-remote-user":  {"mallory"},
		"X-REMOTE-Group": {"devs"},
		"Audit-Id":       {"audit-1"},
		"Content-Type":   {"application/json"},
	}
	if !StripIdentityHeaders(h, "X-Remote-User", "X-Remote-Group", "Audit-ID") {
		t.Fatal("StripIdentityHeaders reported nothing removed")
	}
	if len(h) != 1 || h.Get("Content-Type") != "application/json" {
		t.Fatalf("remaining headers = %v", h)
	}
}
