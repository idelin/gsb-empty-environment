package netsec

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strings"
)

// ClientConfig returns a clone of cfg configured for client-side downgrade
// protection. crypto/tls checks the RFC 8446 ServerHello sentinel when the
// client's maximum version is TLS 1.3. TLS_FALLBACK_SCSV is stripped from the
// first ClientHello; fallback retries add it at the wire layer.
func ClientConfig(cfg *tls.Config) *tls.Config {
	if cfg == nil {
		cfg = &tls.Config{}
	}
	out := cfg.Clone()
	if out.CipherSuites != nil {
		out.CipherSuites = ClientCipherSuites(out.CipherSuites, false)
	}
	return out
}

// NewClientConfig is an alias for ClientConfig.
func NewClientConfig(cfg *tls.Config) *tls.Config {
	return ClientConfig(cfg)
}

// ProtectClientConfig is an alias for ClientConfig.
func ProtectClientConfig(cfg *tls.Config) *tls.Config {
	return ClientConfig(cfg)
}

// Client starts a client-side TLS connection with downgrade protection. This
// is the client entry point for callers that already have a net.Conn. The
// first handshake never carries TLS_FALLBACK_SCSV.
func Client(conn net.Conn, cfg *tls.Config) *tls.Conn {
	return newClientTLSConn(conn, cfg, false)
}

// NewClient is an alias for Client.
func NewClient(conn net.Conn, cfg *tls.Config) *tls.Conn {
	return Client(conn, cfg)
}

// Dial connects to addr and performs one TLS handshake. It never sends
// TLS_FALLBACK_SCSV.
func Dial(network, addr string, cfg *tls.Config) (*tls.Conn, error) {
	return dialAttempt(network, addr, cfg, false, nil)
}

// DialWithFallback performs a first handshake without TLS_FALLBACK_SCSV. If it
// fails, it retries with successively lower maximum versions and adds
// TLS_FALLBACK_SCSV to every retry ClientHello. This is the only path in this
// package that sends the signalling suite.
func DialWithFallback(network, addr string, cfg *tls.Config) (*tls.Conn, error) {
	return dialWithFallback(nil, network, addr, cfg)
}

// DialFallback is an alias for DialWithFallback.
func DialFallback(network, addr string, cfg *tls.Config) (*tls.Conn, error) {
	return DialWithFallback(network, addr, cfg)
}

// DialWithFallbackDialer is DialWithFallback with an explicit underlying
// net.Dialer.
func DialWithFallbackDialer(dialer *net.Dialer, network, addr string, cfg *tls.Config) (*tls.Conn, error) {
	return dialWithFallback(dialer, network, addr, cfg)
}

// DialWithFallbackVersions performs the same protection as DialWithFallback
// but uses the supplied maximum-version sequence. The first entry is the
// first handshake and must not carry TLS_FALLBACK_SCSV.
func DialWithFallbackVersions(network, addr string, cfg *tls.Config, versions ...uint16) (*tls.Conn, error) {
	return dialWithFallbackVersions(nil, network, addr, cfg, versions)
}

// FallbackDialer is a convenience dialer with the same fallback semantics as
// DialWithFallback.
type FallbackDialer struct {
	NetDialer *net.Dialer
	Config    *tls.Config
}

// Dial implements net.Dialer.
func (d *FallbackDialer) Dial(network, addr string) (net.Conn, error) {
	return dialWithFallback(d.NetDialer, network, addr, d.Config)
}

// DialContext implements a context-aware fallback dialer. The context is used
// for the underlying connection attempts.
func (d *FallbackDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	dialer := d.NetDialer
	if dialer == nil {
		dialer = &net.Dialer{}
	}
	return dialWithFallbackContext(ctx, dialer, network, addr, d.Config)
}

func newClientTLSConn(conn net.Conn, cfg *tls.Config, fallbackRetry bool) *tls.Conn {
	protectedConfig := ClientConfig(cfg)
	raw := newClientSentinelConn(conn, maxVersion(protectedConfig), fallbackRetry)
	return tls.Client(raw, protectedConfig)
}

func dialAttempt(network, addr string, cfg *tls.Config, fallbackRetry bool, dialer *net.Dialer) (*tls.Conn, error) {
	return dialAttemptContext(context.Background(), dialer, network, addr, cfg, fallbackRetry)
}

func dialAttemptContext(ctx context.Context, dialer *net.Dialer, network, addr string, cfg *tls.Config, fallbackRetry bool) (*tls.Conn, error) {
	if dialer == nil {
		dialer = &net.Dialer{}
	}
	raw, err := dialer.DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	cfg = clientConfigForAddr(cfg, addr)
	conn := newClientTLSConn(raw, cfg, fallbackRetry)
	if err := conn.HandshakeContext(ctx); err != nil {
		raw.Close()
		return nil, err
	}
	return conn, nil
}

func dialWithFallback(dialer *net.Dialer, network, addr string, cfg *tls.Config) (*tls.Conn, error) {
	return dialWithFallbackContext(context.Background(), dialer, network, addr, cfg)
}

func dialWithFallbackContext(ctx context.Context, dialer *net.Dialer, network, addr string, cfg *tls.Config) (*tls.Conn, error) {
	versions := fallbackVersions(cfg)
	return dialFallbackVersionsContext(ctx, dialer, network, addr, cfg, versions)
}

func dialWithFallbackVersions(dialer *net.Dialer, network, addr string, cfg *tls.Config, versions []uint16) (*tls.Conn, error) {
	return dialFallbackVersionsContext(context.Background(), dialer, network, addr, cfg, versions)
}

func dialFallbackVersionsContext(ctx context.Context, dialer *net.Dialer, network, addr string, cfg *tls.Config, versions []uint16) (*tls.Conn, error) {
	if dialer == nil {
		dialer = &net.Dialer{}
	}
	return dialFallbackVersionsWithFunc(ctx, network, addr, cfg, versions, dialer.DialContext)
}

type rawDialFunc func(context.Context, string, string) (net.Conn, error)

func dialFallbackVersionsWithFunc(ctx context.Context, network, addr string, cfg *tls.Config, versions []uint16, dial rawDialFunc) (*tls.Conn, error) {
	if len(versions) == 0 {
		return nil, fmt.Errorf("netsec: no TLS versions to attempt")
	}
	base := clientConfigForAddr(cfg, addr)
	var lastErr error
	for i, version := range versions {
		attemptCfg := base.Clone()
		attemptCfg.MaxVersion = version
		if i == 0 {
			attemptCfg.CipherSuites = ClientCipherSuites(attemptCfg.CipherSuites, false)
		}
		conn, err := dialAttemptWithFunc(ctx, dial, network, addr, attemptCfg, i > 0)
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("netsec: TLS handshake failed after %d fallback attempts: %w", len(versions), lastErr)
}

func dialAttemptWithFunc(ctx context.Context, dial rawDialFunc, network, addr string, cfg *tls.Config, fallbackRetry bool) (*tls.Conn, error) {
	raw, err := dial(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	ctrl := newClientTLSConn(raw, cfg, fallbackRetry)
	if err := ctrl.HandshakeContext(ctx); err != nil {
		raw.Close()
		return nil, err
	}
	return ctrl, nil
}

func fallbackVersions(cfg *tls.Config) []uint16 {
	max := maxVersion(cfg)
	min := minVersion(cfg)
	if min < tls.VersionTLS10 {
		min = tls.VersionTLS10
	}
	if max > tls.VersionTLS13 {
		max = tls.VersionTLS13
	}
	var out []uint16
	for v := max; v >= min && v >= tls.VersionTLS10; v-- {
		out = append(out, v)
	}
	return out
}

func clientConfigForAddr(cfg *tls.Config, addr string) *tls.Config {
	if cfg == nil {
		cfg = &tls.Config{}
	}
	out := ClientConfig(cfg)
	if out.ServerName == "" {
		host := addr
		if i := strings.LastIndex(addr, ":"); i >= 0 {
			host = addr[:i]
		}
		out.ServerName = host
	}
	return out
}

// clientSentinelConn checks the plaintext ServerHello.random before crypto/tls
// consumes it. It optionally adds TLS_FALLBACK_SCSV to the first ClientHello.
type clientSentinelConn struct {
	net.Conn
	clientMaxVersion uint16
	injectSCSV       bool
	checked          bool
	wrote            bool
	pending          []byte
}

func newClientSentinelConn(conn net.Conn, clientMaxVersion uint16, injectSCSV bool) net.Conn {
	return &clientSentinelConn{
		Conn:             conn,
		clientMaxVersion: clientMaxVersion,
		injectSCSV:       injectSCSV,
	}
}

func (c *clientSentinelConn) Read(p []byte) (int, error) {
	if !c.checked {
		c.checked = true
		record, err := readTLSRecord(c.Conn)
		if err != nil {
			return 0, err
		}
		if parts, parseErr := parseServerHelloRecord(record); parseErr == nil {
			if checkErr := CheckServerRandom(c.clientMaxVersion, parts.negotiatedVersion, parts.random); checkErr != nil {
				_ = sendTLSAlert(c.Conn, AlertIllegalParameter)
				c.Conn.Close()
				return 0, checkErr
			}
		}
		c.pending = record
	}
	if len(c.pending) > 0 {
		n := copy(p, c.pending)
		c.pending = c.pending[n:]
		return n, nil
	}
	return c.Conn.Read(p)
}

func (c *clientSentinelConn) Write(p []byte) (int, error) {
	if !c.injectSCSV || c.wrote {
		return c.Conn.Write(p)
	}
	c.wrote = true
	modified, err := addSCSVToClientHelloRecord(p)
	if err != nil {
		return c.Conn.Write(p)
	}
	if _, err := c.Conn.Write(modified); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *clientSentinelConn) CloseWrite() error {
	if closer, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return closer.CloseWrite()
	}
	return nil
}
