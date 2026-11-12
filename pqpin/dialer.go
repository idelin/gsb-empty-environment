package pqpin

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
)

// Dialer establishes TLS 1.3 connections while enforcing post-quantum
// capability pins. The underlying connection is snooped for ServerHello and
// HelloRetryRequest key_share selections, and every completed hybrid
// handshake refreshes the pin only after the peer's Finished message has been
// verified.
type Dialer struct {
	// Pinner holds the enforced pins. Required.
	Pinner *Pinner
	// TLSConfig is the client configuration. A shallow clone is used per
	// connection with MinVersion pinned to TLS 1.3 and an origin-tracking
	// ClientSessionCache wrapper around the configured cache.
	TLSConfig *tls.Config
	// NetDialer establishes the underlying TCP connection. A zero-value
	// net.Dialer is used when nil.
	NetDialer *net.Dialer

	origins *originCache
}

func (d *Dialer) init() {
	if d.origins == nil {
		d.origins = newOriginCache()
	}
}

// clientConfig returns a per-connection shallow-copied tls.Config.
func (d *Dialer) clientConfig() *tls.Config {
	d.init()
	cfg := d.TLSConfig.Clone()
	if cfg == nil {
		cfg = &tls.Config{}
	}
	cfg.MinVersion = tls.VersionTLS13
	cfg.ClientSessionCache = WrapSessionCache(cfg.ClientSessionCache, d.origins)
	return cfg
}

// sessionKeyFor mirrors crypto/tls's Conn.clientSessionCacheKey: the
// configured ServerName wins, otherwise the remote address string is used.
func sessionKeyFor(tlsConfig *tls.Config, address, serverID string) string {
	if tlsConfig != nil && tlsConfig.ServerName != "" {
		return tlsConfig.ServerName
	}
	if address != "" {
		return address
	}
	return serverID
}

// DialContext connects to address ("host:port") and runs the TLS handshake.
// A detected downgrade yields a *DowngradeError and no usable connection.
// Pins are only committed after a fully completed, Finished-verified hybrid
// handshake.
func (d *Dialer) DialContext(ctx context.Context, network, address string) (*tls.Conn, error) {
	if d.Pinner == nil {
		return nil, errors.New("pqpin: Dialer requires a Pinner")
	}
	d.init()

	netDialer := d.NetDialer
	if netDialer == nil {
		netDialer = &net.Dialer{}
	}
	rawConn, err := netDialer.DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	return d.dialConn(address, rawConn)
}

// Dial connects using DialContext with context.Background.
func (d *Dialer) Dial(network, address string) (*tls.Conn, error) {
	return d.DialContext(context.Background(), network, address)
}

// dialConn runs the TLS handshake over an already-established connection and
// enforces the pin. It is shared by DialContext over real sockets and by the
// net.Pipe-based tests, so both exercise the identical enforcement path.
func (d *Dialer) dialConn(address string, rawConn net.Conn) (*tls.Conn, error) {
	d.init()

	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	serverID := NormalizeServerID(host, port)
	sessionKey := sessionKeyFor(d.TLSConfig, address, serverID)

	// Tear the connection down as soon as an HRR on a pinned server selects a
	// classic group; waiting for Handshake to return would already send key
	// shares on the downgraded path.
	snoop := newSnoopConn(rawConn, func(group uint16) error {
		return d.Pinner.Evaluate(Observation{
			ServerID: serverID,
			HadHRR:   true,
			HRRGroup: group,
		})
	})

	cfg := d.clientConfig()
	tlsConn := tls.Client(snoop, cfg)

	if handshakeErr := tlsConn.Handshake(); handshakeErr != nil {
		_ = tlsConn.Close()
		if de := d.Pinner.Evaluate(snoop.observation(serverID)); de != nil {
			return nil, de
		}
		return nil, handshakeErr
	}

	state := tlsConn.ConnectionState()
	obs := snoop.observation(serverID)
	obs.Resumed = state.DidResume
	if obs.Resumed {
		origin := ResumeOrigin(cfg.ClientSessionCache, sessionKey)
		obs.ResumeHybrid = origin == OriginHybrid
	}

	// Record the final origin before any deferred NewSessionTicket Put runs.
	if origin := originFor(obs); origin != OriginUnknown {
		d.origins.setPending(sessionKey, origin)
	}

	if err := d.Pinner.Evaluate(obs); err != nil {
		_ = tlsConn.Close()
		return nil, err
	}

	// Handshake success means the peer's Finished was verified. Only now may a
	// pin be written, and Commit itself refuses non-hybrid observations.
	if err := d.Pinner.Commit(obs); err != nil {
		var de *DowngradeError
		if !errors.As(err, &de) {
			_ = tlsConn.Close()
			return nil, err
		}
	}
	return tlsConn, nil
}

// originFor decides what origin metadata to attach to a freshly minted
// session ticket. A resumed connection's ticket inherits the resumed
// session's origin; a full handshake records its own negotiated group.
func originFor(obs Observation) Origin {
	if obs.Resumed {
		if obs.ResumeHybrid {
			return OriginHybrid
		}
		return OriginClassic
	}
	if IsHybridGroup(obs.NegotiatedGroup) {
		return OriginHybrid
	}
	return OriginClassic
}
