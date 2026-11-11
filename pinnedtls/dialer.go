package pinnedtls

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
)

// Dialer dials TLS connections through a pinned Verifier. Its zero value is
// not usable; construct one with NewDialer.
type Dialer struct {
	verifier *Verifier
	config   *tls.Config
	dialer   *net.Dialer
	netDial  func(ctx context.Context, network, addr string) (net.Conn, error)
}

// NewDialer returns a Dialer that applies verifier on every handshake. The
// config is cloned and the verifier bound to the clone, so the caller's config
// is not mutated. A bound ClientSessionCache is installed automatically.
func NewDialer(verifier *Verifier, config *tls.Config) (*Dialer, error) {
	if verifier == nil {
		return nil, errors.New("pinnedtls: verifier must not be nil")
	}
	if config == nil {
		config = &tls.Config{}
	}

	cloned := config.Clone()
	if _, err := verifier.Bind(cloned); err != nil {
		return nil, err
	}

	return &Dialer{
		verifier: verifier,
		config:   cloned,
		dialer:   &net.Dialer{},
	}, nil
}

// Config returns the cloned tls.Config the dialer uses. It must not be
// replaced; pin configuration rotates through Verifier.Update instead.
func (d *Dialer) Config() *tls.Config { return d.config }

// DialContext establishes a TLS connection to addr (for example "host:443").
//
// If a cached session fails pin verification during resumption -- the normal
// case after a pin rotation retires the key pinned by an old ticket -- the
// stale session is evicted and the connection is retried exactly once with a
// full handshake, which evaluates the current pins against the freshly
// verified chain. Failures on full handshakes are returned without retry.
func (d *Dialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	conn, err := d.handshake(ctx, network, addr)
	if err == nil {
		return conn, nil
	}

	var pinErr *PinError
	if !errors.As(err, &pinErr) || !pinErr.DidResume {
		return nil, err
	}

	// A resumed handshake carries no new certificates: its chains were
	// restored from the ticket. Drop the ticket and force a full handshake.
	d.verifier.EvictSession(pinErr.ServerName)

	if conn != nil {
		_ = conn.Close()
	}
	return d.handshake(ctx, network, addr)
}

func (d *Dialer) handshake(ctx context.Context, network, addr string) (net.Conn, error) {
	dialRaw := d.netDial
	if dialRaw == nil {
		dialRaw = d.dialer.DialContext
	}
	rawConn, err := dialRaw(ctx, network, addr)
	if err != nil {
		return nil, err
	}

	config := d.config
	if config.ServerName == "" {
		config = config.Clone()
		config.ServerName = serverNameFromAddr(addr)
	}

	conn := tls.Client(rawConn, config)
	if err := conn.HandshakeContext(ctx); err != nil {
		_ = rawConn.Close()
		return nil, err
	}
	return conn, nil
}

func serverNameFromAddr(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

// Dial is DialContext with context.Background.
func (d *Dialer) Dial(network, addr string) (net.Conn, error) {
	return d.DialContext(context.Background(), network, addr)
}
