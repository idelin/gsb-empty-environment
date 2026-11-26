package rpki

import (
	"crypto/x509"
	"errors"
	"fmt"
	"math/big"
	"time"
)

var (
	ErrCRL          = errors.New("rpki: CRL validation failed")
	ErrCRLSignature = errors.New("rpki: CRL signature invalid")
	ErrRevoked      = errors.New("rpki: certificate is revoked")
)

// ValidatedCRL is a CRL whose issuer signature and freshness were checked.
type ValidatedCRL struct {
	RL     *x509.RevocationList
	Issuer *CertInfo
}

// ParseAndVerifyCRL parses a DER CRL and verifies the CA signature.
func ParseAndVerifyCRL(der []byte, issuer *CertInfo, now time.Time) (*ValidatedCRL, error) {
	rl, err := x509.ParseRevocationList(der)
	if err != nil {
		return nil, fmt.Errorf("%w: parse: %v", ErrCRL, err)
	}
	if issuer == nil || issuer.X509 == nil {
		return nil, fmt.Errorf("%w: missing issuer", ErrCRL)
	}
	if err := rl.CheckSignatureFrom(issuer.X509); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCRLSignature, err)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	if now.Before(rl.ThisUpdate) {
		return nil, fmt.Errorf("%w: CRL thisUpdate is in the future", ErrCRL)
	}
	if !rl.NextUpdate.IsZero() && now.After(rl.NextUpdate) {
		return nil, fmt.Errorf("%w: CRL nextUpdate %s has passed", ErrCRL, rl.NextUpdate.Format(time.RFC3339))
	}
	return &ValidatedCRL{RL: rl, Issuer: issuer}, nil
}

// IsRevoked reports whether serial appears on the CRL.
func (c *ValidatedCRL) IsRevoked(serial *big.Int) (time.Time, bool) {
	if c == nil || serial == nil {
		return time.Time{}, false
	}
	for _, entry := range c.RL.RevokedCertificateEntries {
		if entry.SerialNumber.Cmp(serial) == 0 {
			return entry.RevocationTime, true
		}
	}
	for _, sn := range c.RL.RevokedCertificates {
		if sn.SerialNumber.Cmp(serial) == 0 {
			return sn.RevocationTime, true
		}
	}
	return time.Time{}, false
}

// RequireNotRevoked returns ErrRevoked when cert is listed.
func (c *ValidatedCRL) RequireNotRevoked(cert *CertInfo) error {
	if cert == nil || cert.X509 == nil {
		return fmt.Errorf("%w: nil certificate", ErrRevoked)
	}
	if when, revoked := c.IsRevoked(cert.X509.SerialNumber); revoked {
		return fmt.Errorf("%w: serial %s revoked at %s", ErrRevoked, cert.X509.SerialNumber, when.UTC().Format(time.RFC3339))
	}
	return nil
}
