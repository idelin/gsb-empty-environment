package pinning

import (
	"crypto/x509"
	"errors"
	"fmt"
	"time"
)

// ErrNoActivePolicy is returned when no published policy is in force at
// the verification time, for example when every policy has expired.
var ErrNoActivePolicy = errors.New("pinning: no active policy")

// ErrNoPinnedChain is returned when every candidate chain contains at
// least one certificate that is not declared in the pin set.
var ErrNoPinnedChain = errors.New("pinning: no presented chain is fully pinned")

// Verify checks a presented peer certificate chain for purpose against
// the policies active at the current time. serverName is the expected
// DNS name of the peer. The chain is built exclusively from the roots
// declared by each policy; there is no fallback to the system trust
// store. Every certificate of a candidate chain, leaf first, must be
// declared in the purpose pin set or the chain is rejected.
func (m *Manager) Verify(purpose Purpose, serverName string, rawCerts [][]byte) error {
	if len(rawCerts) == 0 {
		return errors.New("pinning: peer presented no certificates")
	}
	certs := make([]*x509.Certificate, 0, len(rawCerts))
	for i, raw := range rawCerts {
		cert, err := x509.ParseCertificate(raw)
		if err != nil {
			return fmt.Errorf("pinning: parsing certificate %d: %w", i, err)
		}
		certs = append(certs, cert)
	}

	now := m.now()
	policies := m.activePolicies(now)
	if len(policies) == 0 {
		return ErrNoActivePolicy
	}

	var errs []error
	for _, p := range policies {
		if err := verifyWithPolicy(p, purpose, serverName, certs, now); err != nil {
			errs = append(errs, fmt.Errorf("policy %s: %w", p.Version, err))
			continue
		}
		return nil
	}
	return errors.Join(errs...)
}

func verifyWithPolicy(p *Policy, purpose Purpose, serverName string, certs []*x509.Certificate, now time.Time) error {
	pins, ok := p.Pins[purpose]
	if !ok {
		return fmt.Errorf("no pin set declared for purpose %q", purpose)
	}
	intermediates := x509.NewCertPool()
	for _, cert := range certs[1:] {
		intermediates.AddCert(cert)
	}
	opts := x509.VerifyOptions{
		DNSName:       serverName,
		Roots:         p.Roots,
		Intermediates: intermediates,
		CurrentTime:   now,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	chains, err := certs[0].Verify(opts)
	if err != nil {
		return fmt.Errorf("chain building failed: %w", err)
	}
	for _, chain := range chains {
		if chainPinned(chain, pins) {
			return nil
		}
	}
	return ErrNoPinnedChain
}

// chainPinned reports whether every certificate in chain is declared in
// pins: the leaf against LeafKeys, all issuers against IssuerKeys.
func chainPinned(chain []*x509.Certificate, pins *PinSet) bool {
	for i, cert := range chain {
		if i == 0 {
			if !pins.allowsLeaf(cert) {
				return false
			}
			continue
		}
		if !pins.allowsIssuer(cert) {
			return false
		}
	}
	return true
}
