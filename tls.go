package workloadid

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
)

// TLS errors.
var (
	ErrResumeNotAllowed = errors.New("workloadid: session resumption disabled, full re-authentication required")
	ErrHandshakeDenied  = errors.New("workloadid: connection authorization failed")
)

// TLSAuthorizer wires the Verifier into crypto/tls so policy is evaluated inside
// VerifyConnection, which runs for both fresh handshakes and any resumed
// session.
type TLSAuthorizer struct {
	Verifier     *Verifier
	CallerPolicy *Policy
	TargetPolicy *Policy
	// Attest supplies the peer attestation claim per connection.
	Attest func() *NodeAttestationClaim
	// ServerName is enforced against the peer DNS SAN when non-empty.
	ServerName string
	// AllowResumption opts in to TLS session resumption. When enabled, every
	// resumed connection is still re-authorized through AuthenticateResumed and
	// both policies are re-evaluated against the current generation. The default
	// (false) disables tickets and the session cache entirely.
	AllowResumption bool
}

// TLSConfig returns a tls.Config that presents the rotating local credential
// and enforces mTLS with per-connection authorization.
func (a *TLSAuthorizer) TLSConfig(manager *Manager) (*tls.Config, error) {
	current, err := manager.Current()
	if err != nil {
		return nil, err
	}
	cfg := &tls.Config{
		ClientAuth:             tls.RequireAndVerifyClientCert,
		MinVersion:             tls.VersionTLS12,
		SessionTicketsDisabled: !a.AllowResumption,
		// Provide the bundle roots so crypto/tls builds and verifies the chain;
		// workload authorization is then enforced in VerifyConnection.
		RootCAs:   current.Roots,
		ClientCAs: current.Roots,
		// SNI drives client-side hostname verification; VerifyConnection also
		// enforces ExpectedServerName against the peer DNS SAN.
		ServerName:       a.ServerName,
		VerifyConnection: a.verifyConnection,
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			return manager.tlsCertificate(current)
		},
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			return manager.tlsCertificate(current)
		},
	}
	if !a.AllowResumption {
		cfg.ClientSessionCache = nil
	}
	return cfg, nil
}

func (a *TLSAuthorizer) verifyConnection(cs tls.ConnectionState) error {
	if cs.DidResume && !a.AllowResumption {
		return ErrResumeNotAllowed
	}
	var attestation *NodeAttestationClaim
	if a.Attest != nil {
		attestation = a.Attest()
	}
	_, err := a.Verifier.Authorize(AuthRequest{
		RawCerts:           rawChain(cs),
		Resumed:            cs.DidResume,
		CallerPolicy:       a.CallerPolicy,
		TargetPolicy:       a.TargetPolicy,
		Attestation:        attestation,
		ExpectedServerName: a.ServerName,
	})
	if err != nil {
		return errors.Join(ErrHandshakeDenied, err)
	}
	return nil
}

// rawChain returns the DER certificates available on the connection. For a
// resumed handshake only the leaf is retained by crypto/tls, which is exactly
// what AuthenticateResumed consumes.
func rawChain(cs tls.ConnectionState) [][]byte {
	if len(cs.PeerCertificates) > 0 {
		raw := make([][]byte, 0, len(cs.PeerCertificates))
		for _, cert := range cs.PeerCertificates {
			raw = append(raw, cert.Raw)
		}
		return raw
	}
	return nil
}

func (m *Manager) tlsCertificate(snap *Snapshot) (*tls.Certificate, error) {
	credential, err := m.Credential()
	if err != nil {
		return nil, err
	}
	cert := [][]byte{credential.Leaf.Raw}
	// Attach bundle intermediates so the peer can build the chain even if it
	// only trusts the root.
	for _, intermediate := range snap.intermediateDERs() {
		cert = append(cert, intermediate)
	}
	return &tls.Certificate{
		Certificate: cert,
		PrivateKey:  credential.PrivateKey,
		Leaf:        credential.Leaf,
	}, nil
}

// PeerPool exposes the active root pool for callers building their own
// tls.Config while still using this component for VerifyConnection.
func (m *Manager) PeerPool() (*x509.CertPool, error) {
	snap, err := m.Current()
	if err != nil {
		return nil, err
	}
	return snap.Roots, nil
}
