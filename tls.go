package streamguard

import (
	"crypto/tls"
	"net"
)

// UpgradeServerConn discards guarded pre-TLS bytes and performs a server-side
// TLS handshake. Call it after sending the XMPP <proceed/> response.
func (g *Guard) UpgradeServerConn(conn net.Conn, config *tls.Config) (*tls.Conn, error) {
	if err := g.UpgradeTLS(); err != nil {
		return nil, err
	}

	tlsConn := tls.Server(conn, config)
	if err := tlsConn.Handshake(); err != nil {
		g.fail(err)
		return nil, err
	}
	return tlsConn, nil
}

// UpgradeClientConn discards guarded pre-TLS bytes and performs a client-side
// TLS handshake after receiving <proceed/>.
func (g *Guard) UpgradeClientConn(conn net.Conn, config *tls.Config) (*tls.Conn, error) {
	if err := g.UpgradeTLS(); err != nil {
		return nil, err
	}

	tlsConn := tls.Client(conn, config)
	if err := tlsConn.Handshake(); err != nil {
		g.fail(err)
		return nil, err
	}
	return tlsConn, nil
}
