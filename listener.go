package netsec

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"sync/atomic"
	"time"
)

const defaultHandshakeTimeout = 10 * time.Second

var _ net.Listener = (*Listener)(nil)

// Listener performs the TLS handshake before Accept returns.
//
// This ordering is security-relevant. A raw tls.Listener.Accept returns a
// connection before its handshake has completed, which lets an application
// write a protocol banner (for example SMTP's "220 ...") to a client whose
// ALPN or certificate is later rejected. Listener closes failed handshakes and
// keeps accepting, so application code can never write before authentication
// of the TLS handshake.
type Listener struct {
	inner            net.Listener
	config           *tls.Config
	policy           Policy
	HandshakeTimeout time.Duration
}

// NewListener wraps inner with the strict ALPN policy.
func NewListener(inner net.Listener, base *tls.Config, policy Policy) (*Listener, error) {
	if inner == nil {
		return nil, fmt.Errorf("alpn: nil listener")
	}
	config, err := ConfigureTLS(base, policy)
	if err != nil {
		return nil, err
	}
	return &Listener{
		inner:            inner,
		config:           config,
		policy:           policy.clone(),
		HandshakeTimeout: defaultHandshakeTimeout,
	}, nil
}

// Accept waits for a TCP connection, completes its TLS handshake, and only
// then returns it. Failed handshakes are dropped and do not terminate the
// listener.
func (l *Listener) Accept() (net.Conn, error) {
	for {
		raw, err := l.inner.Accept()
		if err != nil {
			return nil, err
		}
		conn, err := l.handshake(raw)
		if err != nil {
			raw.Close()
			continue
		}
		return conn, nil
	}
}

func (l *Listener) handshake(raw net.Conn) (net.Conn, error) {
	wrapped := &alertConn{Conn: raw}
	conn := tls.Server(wrapped, l.config)

	timeout := l.HandshakeTimeout
	if timeout <= 0 {
		timeout = defaultHandshakeTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	if err := conn.HandshakeContext(ctx); err != nil {
		return nil, err
	}

	state := conn.ConnectionState()
	if state.NegotiatedProtocol == "" {
		if l.policy.LegacyProtocol == "" {
			return nil, ErrNoApplicationProtocol
		}
	} else if !l.policy.accepts(state.NegotiatedProtocol) {
		return nil, fmt.Errorf("%w: negotiated protocol %q is not allowed", ErrNoApplicationProtocol, state.NegotiatedProtocol)
	}
	return conn, nil
}

// Close closes the underlying listener.
func (l *Listener) Close() error {
	return l.inner.Close()
}

// Addr returns the underlying listener address.
func (l *Listener) Addr() net.Addr {
	return l.inner.Addr()
}

// alertConn rewrites crypto/tls's internal_error alert to the RFC 7301
// no_application_protocol alert when the ALPN callback has just rejected a
// ClientHello that omitted ALPN. This path is pre-handshake and therefore
// unencrypted.
type alertConn struct {
	net.Conn
	rejectNoApplicationProtocol atomic.Bool
}

func (c *alertConn) markNoApplicationProtocol() {
	c.rejectNoApplicationProtocol.Store(true)
}

func (c *alertConn) Write(p []byte) (int, error) {
	if c.rejectNoApplicationProtocol.CompareAndSwap(true, false) {
		// TLS alert record: type=alert, length=2, level=fatal,
		// description=internal_error (80) -> no_application_protocol (120).
		if len(p) == 7 && p[0] == 21 && p[5] == 2 && p[6] == 80 {
			rewritten := append([]byte(nil), p...)
			rewritten[6] = 120
			p = rewritten
		}
	}
	return c.Conn.Write(p)
}
