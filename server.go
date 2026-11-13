package netsec

import (
	"crypto/tls"
	"net"
)

// ServerConfig returns a clone of cfg configured for server-side downgrade
// protection. crypto/tls writes the RFC 8446 ServerHello sentinel itself
// based on cfg.MaxVersion, so the clone preserves MaxVersion.
func ServerConfig(cfg *tls.Config) *tls.Config {
	if cfg == nil {
		cfg = &tls.Config{}
	}
	return cfg.Clone()
}

// NewServerConfig is an alias for ServerConfig.
func NewServerConfig(cfg *tls.Config) *tls.Config {
	return ServerConfig(cfg)
}

// ProtectServerConfig is an alias for ServerConfig.
func ProtectServerConfig(cfg *tls.Config) *tls.Config {
	return ServerConfig(cfg)
}

// Server starts a server-side TLS connection with downgrade protection. It is
// the server entry point for callers that already have a net.Conn.
func Server(conn net.Conn, cfg *tls.Config) *tls.Conn {
	protectedConfig := ServerConfig(cfg)
	return tls.Server(newServerSCSVConn(conn, maxVersion(protectedConfig)), protectedConfig)
}

// NewServer is an alias for Server.
func NewServer(conn net.Conn, cfg *tls.Config) *tls.Conn {
	return Server(conn, cfg)
}

// Listen listens on the given network address and returns a TLS listener with
// downgrade protection.
func Listen(network, laddr string, cfg *tls.Config) (net.Listener, error) {
	ln, err := net.Listen(network, laddr)
	if err != nil {
		return nil, err
	}
	return NewListener(ln, cfg), nil
}

// NewListener wraps ln with downgrade protection. The returned listener
// accepts *tls.Conn values, like tls.NewListener.
func NewListener(ln net.Listener, cfg *tls.Config) net.Listener {
	protectedConfig := ServerConfig(cfg)
	return tls.NewListener(
		&serverSCSVListener{Listener: ln, maxVersion: maxVersion(protectedConfig)},
		protectedConfig,
	)
}

type serverSCSVListener struct {
	net.Listener
	maxVersion uint16
}

func (l *serverSCSVListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return newServerSCSVConn(conn, l.maxVersion), nil
}

// serverSCSVConn validates TLS_FALLBACK_SCSV before crypto/tls negotiates the
// protocol version. For a valid fallback, it removes the signalling suite so
// the server transcript matches the client's transcript. For an invalid one,
// it leaves the ClientHello untouched and crypto/tls emits
// inappropriate_fallback.
type serverSCSVConn struct {
	net.Conn
	maxVersion uint16
	checked    bool
	pending    []byte
}

func newServerSCSVConn(conn net.Conn, maxVersion uint16) net.Conn {
	return &serverSCSVConn{Conn: conn, maxVersion: maxVersion}
}

func (c *serverSCSVConn) Read(p []byte) (int, error) {
	if !c.checked {
		c.checked = true
		record, err := readTLSRecord(c.Conn)
		if err != nil {
			return 0, err
		}
		c.pending = record
		if parts, parseErr := parseClientHelloRecord(record); parseErr == nil && HasFallbackSCSV(parts.cipherSuites) {
			if CheckFallbackSCSV(c.maxVersion, parts.legacyVersion, parts.cipherSuites) == nil {
				if cleaned, cleanErr := removeSCSVFromClientHelloRecord(record); cleanErr == nil {
					c.pending = cleaned
				}
			}
		}
	}
	if len(c.pending) > 0 {
		n := copy(p, c.pending)
		c.pending = c.pending[n:]
		return n, nil
	}
	return c.Conn.Read(p)
}

func (c *serverSCSVConn) CloseWrite() error {
	if closer, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return closer.CloseWrite()
	}
	return nil
}
