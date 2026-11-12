package netsec

import (
	"crypto/tls"
	"crypto/x509"
	"sync"
)

// SNICertTracker records which server certificate was selected for each TLS
// server name and resolves it back per connection. Because a single host can
// legitimately terminate many concurrent handshakes with the same certificate,
// keying by (canonicalized) SNI is sufficient and avoids coupling to internal
// net/http connection identifiers.
//
// Usage:
//
//	tracker := NewSNICertTracker()
//	tlsConfig.GetCertificate = tracker.GetCertificate // statically registered
//	binder := New(WithLeafResolver(tracker))
type SNICertTracker struct {
	mu     sync.RWMutex
	byName map[string]*x509.Certificate
	static map[string]*tls.Certificate
}

// NewSNICertTracker creates an empty tracker.
func NewSNICertTracker() *SNICertTracker {
	return &SNICertTracker{
		byName: make(map[string]*x509.Certificate),
		static: make(map[string]*tls.Certificate),
	}
}

// Register associates a certificate with one or more host names (SNI values).
// The leaf is parsed lazily if cert.Leaf is nil. Registration is typically
// performed once at startup alongside the reverse proxy vhost configuration.
func (t *SNICertTracker) Register(cert *tls.Certificate, names ...string) error {
	leaf, err := leafOf(cert)
	if err != nil {
		return err
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	for _, name := range names {
		canonical, err := canonicalHost(name)
		if err != nil {
			return err
		}
		t.byName[canonical] = leaf
		t.static[canonical] = cert
	}
	return nil
}

// GetCertificate is a drop-in tls.Config.GetCertificate callback backed by the
// registered certificates. It matches the ClientHello SNI after the same
// canonicalization used by the middleware.
func (t *SNICertTracker) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	if hello == nil || hello.ServerName == "" {
		return nil, errNoCertificate
	}
	canonical, err := canonicalHost(hello.ServerName)
	if err != nil {
		return nil, err
	}

	t.mu.RLock()
	cert, ok := t.static[canonical]
	t.mu.RUnlock()
	if !ok {
		return nil, errNoCertificate
	}
	return cert, nil
}

// Observe wraps an existing dynamic GetCertificate callback. The returned
// callback records every successfully selected certificate against the
// ClientHello SNI before returning it, letting deployments keep their own
// cert-loading logic while feeding the binder.
func (t *SNICertTracker) Observe(next func(*tls.ClientHelloInfo) (*tls.Certificate, error)) func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	return func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		cert, err := next(hello)
		if err != nil || cert == nil || hello == nil || hello.ServerName == "" {
			return cert, err
		}
		leaf, leafErr := leafOf(cert)
		if leafErr != nil {
			return cert, leafErr
		}
		if canonical, canonErr := canonicalHost(hello.ServerName); canonErr == nil {
			t.mu.Lock()
			t.byName[canonical] = leaf
			t.mu.Unlock()
		}
		return cert, err
	}
}

// LeafForConnection implements LeafResolver.
func (t *SNICertTracker) LeafForConnection(state *tls.ConnectionState) (*x509.Certificate, error) {
	if state == nil || state.ServerName == "" {
		return nil, nil
	}
	canonical, err := canonicalHost(state.ServerName)
	if err != nil {
		return nil, err
	}
	t.mu.RLock()
	leaf := t.byName[canonical]
	t.mu.RUnlock()
	return leaf, nil
}

var errNoCertificate = &certificateError{}

type certificateError struct{}

func (e *certificateError) Error() string { return "netsec: no certificate registered for server name" }

func leafOf(cert *tls.Certificate) (*x509.Certificate, error) {
	if cert == nil {
		return nil, errNoCertificate
	}
	if cert.Leaf != nil {
		return cert.Leaf, nil
	}
	if len(cert.Certificate) == 0 {
		return nil, errNoCertificate
	}
	parsed, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, err
	}
	cert.Leaf = parsed
	return parsed, nil
}
