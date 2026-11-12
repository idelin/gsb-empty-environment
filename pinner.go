package netsec

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"sync"
	"time"
)

// Pinner performs TOFU certificate pinning for TLS connections.
type Pinner struct {
	roots *x509.CertPool
	store PinStore
	now   func() time.Time

	mu sync.Mutex
}

// Option configures a Pinner.
type Option func(*Pinner)

// WithStore configures the pin store. A new MemoryStore is used by default.
func WithStore(store PinStore) Option {
	return func(p *Pinner) {
		if store != nil {
			p.store = store
		}
	}
}

// WithClock configures the default clock used by CheckConn. Checks that take
// an explicit time.Time are unaffected.
func WithClock(now func() time.Time) Option {
	return func(p *Pinner) {
		if now != nil {
			p.now = now
		}
	}
}

// WithRoots configures the certificate pool trusted by the caller for
// accepting a changed certificate.
func WithRoots(roots *x509.CertPool) Option {
	return func(p *Pinner) {
		p.roots = roots
	}
}

// WithTrustedRoots is an alias for WithRoots.
func WithTrustedRoots(roots *x509.CertPool) Option {
	return WithRoots(roots)
}

// NewPinner returns a pinner that trusts roots when validating a changed
// certificate and stores pins in a new MemoryStore by default.
//
// If roots is nil and no roots option is supplied, no roots are trusted and a
// certificate change is accepted only after the stored certificate expires.
func NewPinner(roots *x509.CertPool, opts ...Option) *Pinner {
	p := &Pinner{
		store: NewMemoryStore(),
		now:   time.Now,
	}
	p.roots = roots
	for _, opt := range opts {
		if opt != nil {
			opt(p)
		}
	}
	if p.roots == nil {
		p.roots = x509.NewCertPool()
	}
	return p
}

// New is an alias for NewPinner.
func New(roots *x509.CertPool, opts ...Option) *Pinner {
	return NewPinner(roots, opts...)
}

// NewPinnerWithStore returns a pinner with the supplied store and clock.
// A nil clock means time.Now.
func NewPinnerWithStore(roots *x509.CertPool, store PinStore, now func() time.Time) *Pinner {
	opts := []Option{WithStore(store)}
	if now != nil {
		opts = append(opts, WithClock(now))
	}
	return NewPinner(roots, opts...)
}

// Store returns the pin store used by p.
func (p *Pinner) Store() PinStore {
	return p.store
}

// Lookup returns the pin currently recorded for host and port.
func (p *Pinner) Lookup(host string, port int) (Pin, bool, error) {
	return p.store.Get(canonicalHost(host), port)
}

// Check checks a connection that has already completed its handshake. The
// connected hostname and port must be supplied explicitly because a
// tls.Conn's remote address does not retain the original DNS name.
//
// A successful check records the pin only after all checks pass and only when
// the handshake is complete.
func (p *Pinner) Check(conn *tls.Conn, host string, port int, now time.Time) error {
	return p.CheckConnection(conn, host, port, now)
}

// Verify is an alias for CheckConnection.
func (p *Pinner) Verify(conn *tls.Conn, host string, port int, now time.Time) error {
	return p.CheckConnection(conn, host, port, now)
}

// CheckConnection checks an already established TLS connection.
func (p *Pinner) CheckConnection(conn *tls.Conn, host string, port int, now time.Time) error {
	if conn == nil {
		return fmt.Errorf("%w: nil TLS connection", ErrInvalidHostPort)
	}
	return p.CheckState(host, port, conn.ConnectionState(), now)
}

// CheckConn derives the hostname from the TLS ServerName and the port from the
// remote address, then checks an already established connection. If ServerName
// is empty, the remote IP address is used as the hostname.
func (p *Pinner) CheckConn(conn *tls.Conn, now time.Time) error {
	if conn == nil {
		return fmt.Errorf("%w: nil TLS connection", ErrInvalidHostPort)
	}
	state := conn.ConnectionState()
	host := state.ServerName
	port := 0
	if addr, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
		if host == "" {
			host = addr.IP.String()
		}
		port = addr.Port
	} else if host == "" && conn.RemoteAddr() != nil {
		host = conn.RemoteAddr().String()
	}
	return p.CheckState(host, port, state, now)
}

// CheckConnNow is like CheckConn but uses the configured clock.
func (p *Pinner) CheckConnNow(conn *tls.Conn) error {
	return p.CheckConn(conn, p.now())
}

// CheckState checks a TLS connection state against the pin for host and port.
// The state must report a completed handshake.
func (p *Pinner) CheckState(host string, port int, state tls.ConnectionState, now time.Time) error {
	return p.CheckCertificates(host, port, state.PeerCertificates, now, state.HandshakeComplete)
}

// CheckCertificate checks a leaf certificate and optionally its chain. It is
// useful when a caller has already parsed the peer certificate rather than
// retained a tls.ConnectionState. handshakeComplete must be true before any
// pin is written.
func (p *Pinner) CheckCertificate(host string, port int, leaf *x509.Certificate, chain []*x509.Certificate, now time.Time, handshakeComplete bool) error {
	certs := make([]*x509.Certificate, 0, len(chain)+1)
	if leaf != nil {
		certs = append(certs, leaf)
	}
	certs = append(certs, chain...)
	return p.CheckCertificates(host, port, certs, now, handshakeComplete)
}

// CheckCertificates is the common implementation for the connection checks.
func (p *Pinner) CheckCertificates(host string, port int, certs []*x509.Certificate, now time.Time, handshakeComplete bool) error {
	if !handshakeComplete {
		return ErrHandshakeIncomplete
	}
	if !validPort(port) {
		return fmt.Errorf("%w: port %d", ErrInvalidHostPort, port)
	}
	host = canonicalHost(host)
	if host == "" {
		return fmt.Errorf("%w: empty hostname", ErrInvalidHostPort)
	}
	if len(certs) == 0 || certs[0] == nil {
		return ErrNoPeerCertificate
	}
	leaf := certs[0]

	if err := leaf.VerifyHostname(host); err != nil {
		return fmt.Errorf("%w: %v", ErrHostnameMismatch, err)
	}

	pending := Pin{
		Host:        host,
		Port:        port,
		Fingerprint: SPKIFingerprint(leaf),
		FirstSeen:   now,
		NotAfter:    leaf.NotAfter,
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	stored, ok, err := p.store.Get(host, port)
	if err != nil {
		return fmt.Errorf("netsec: load pin for %s:%d: %w", host, port, err)
	}

	if !ok {
		if err := p.store.Put(pending); err != nil {
			return fmt.Errorf("netsec: store pin for %s:%d: %w", host, port, err)
		}
		return nil
	}

	if stored.Fingerprint == pending.Fingerprint {
		return nil
	}

	if !stored.ExpiredAt(now) {
		if err := p.verifyChain(leaf, certs[1:], host, now); err != nil {
			return &FingerprintChangedError{
				Host:            host,
				Port:            port,
				Stored:          stored.Fingerprint,
				Presented:       pending.Fingerprint,
				StoredFirstSeen: stored.FirstSeen,
				StoredNotAfter:  stored.NotAfter,
				ChainError:      err,
			}
		}
	}

	if err := p.store.Put(pending); err != nil {
		return fmt.Errorf("netsec: store pin for %s:%d: %w", host, port, err)
	}
	return nil
}

func (p *Pinner) verifyChain(leaf *x509.Certificate, chain []*x509.Certificate, host string, now time.Time) error {
	opts := x509.VerifyOptions{
		Roots:       p.roots,
		CurrentTime: now,
		KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSName:     host,
	}
	if len(chain) > 0 {
		intermediates := x509.NewCertPool()
		for _, cert := range chain {
			if cert != nil {
				intermediates.AddCert(cert)
			}
		}
		opts.Intermediates = intermediates
	}
	_, err := leaf.Verify(opts)
	return err
}
