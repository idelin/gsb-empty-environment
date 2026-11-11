package ticketkeys

import (
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"sync"
)

// Policy tracks revoked client certificate fingerprints and a policy
// generation counter. Tickets issued under an older generation, or bound to
// a revoked fingerprint, are rejected at resumption time.
//
// The zero value is ready to use.
type Policy struct {
	mu         sync.RWMutex
	revoked    map[[32]byte]struct{}
	generation uint64
}

// NewPolicy returns an empty Policy.
func NewPolicy() *Policy {
	return &Policy{revoked: make(map[[32]byte]struct{})}
}

// Fingerprint returns the SHA-256 fingerprint of a certificate, the value
// tickets are bound to and revocations are keyed by.
func Fingerprint(cert *x509.Certificate) [32]byte {
	return sha256.Sum256(cert.Raw)
}

// Revoke marks a certificate fingerprint as revoked.
func (p *Policy) Revoke(fp [32]byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.revoked == nil {
		p.revoked = make(map[[32]byte]struct{})
	}
	p.revoked[fp] = struct{}{}
}

// IsRevoked reports whether a certificate fingerprint has been revoked.
func (p *Policy) IsRevoked(fp [32]byte) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	_, ok := p.revoked[fp]
	return ok
}

// Generation returns the current policy generation.
func (p *Policy) Generation() uint64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.generation
}

// BumpGeneration invalidates every ticket issued so far and returns the new
// generation.
func (p *Policy) BumpGeneration() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.generation++
	return p.generation
}

// VerifyPeerCertificate can be assigned to tls.Config.VerifyPeerCertificate
// to also reject revoked client certificates during full handshakes. Note
// that resumed handshakes never re-present the certificate, which is why
// the revocation check inside UnwrapSession is the critical one.
func (p *Policy) VerifyPeerCertificate(rawCerts [][]byte, _ [][]*x509.Certificate) error {
	if len(rawCerts) == 0 {
		return nil
	}
	if p.IsRevoked(sha256.Sum256(rawCerts[0])) {
		return errors.New("ticketkeys: client certificate revoked")
	}
	return nil
}
