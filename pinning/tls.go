package pinning

import (
	"crypto/tls"
	"crypto/x509"
)

// TLSConfig returns a tls.Config for a gateway client connection to
// serverName that enforces the pin policies held by the manager for
// purpose. Standard chain verification is disabled and replaced by the
// manager, so a chain that is not fully pinned fails the handshake even
// if it would be trusted by the system store.
func (m *Manager) TLSConfig(purpose Purpose, serverName string) *tls.Config {
	return &tls.Config{
		ServerName:         serverName,
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true, // verification is performed exclusively by VerifyPeerCertificate below.
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			return m.Verify(purpose, serverName, rawCerts)
		},
	}
}
