package netsec

import (
	"crypto/x509"
	"fmt"
	"math/big"
	"time"
)

// Revocation identifies a certificate by issuer and serial number. Revocation
// lists are full replacements scoped to a trust domain; a newer generation
// always replaces the older list in its entirety.
type Revocation struct {
	TrustDomain  TrustDomain
	Issuer       []byte
	SerialNumber *big.Int
	RevokedAt    time.Time
	Reason       string
}

// RevocationForCertificate constructs a revocation entry for a leaf certificate.
func RevocationForCertificate(td TrustDomain, cert *x509.Certificate, at time.Time) (Revocation, error) {
	canonicalTD, err := ParseTrustDomain(string(td))
	if err != nil {
		return Revocation{}, err
	}
	if cert == nil {
		return Revocation{}, fmt.Errorf("%w: nil certificate", ErrNoCertificate)
	}
	if cert.SerialNumber == nil || cert.SerialNumber.Sign() <= 0 {
		return Revocation{}, fmt.Errorf("%w: invalid certificate serial", ErrInvalidCertificate)
	}
	if len(cert.RawIssuer) == 0 {
		return Revocation{}, fmt.Errorf("%w: certificate has no issuer", ErrInvalidCertificate)
	}
	return Revocation{
		TrustDomain:  canonicalTD,
		Issuer:       append([]byte(nil), cert.RawIssuer...),
		SerialNumber: new(big.Int).Set(cert.SerialNumber),
		RevokedAt:    at,
	}, nil
}

type revocationKey struct {
	issuer string
	serial string
}

type revocationState struct {
	generation Generation
	entries    map[revocationKey]Revocation
}

func compileRevocations(td TrustDomain, entries []Revocation) (map[revocationKey]Revocation, error) {
	out := make(map[revocationKey]Revocation, len(entries))
	for i, entry := range entries {
		if entry.TrustDomain != "" {
			canonical, err := ParseTrustDomain(string(entry.TrustDomain))
			if err != nil {
				return nil, fmt.Errorf("%w: revocation %d: %v", ErrInvalidConfig, i, err)
			}
			if canonical != td {
				return nil, fmt.Errorf("%w: revocation %d belongs to trust domain %q, expected %q", ErrInvalidConfig, i, canonical, td)
			}
		}
		if len(entry.Issuer) == 0 {
			return nil, fmt.Errorf("%w: revocation %d has empty issuer", ErrInvalidConfig, i)
		}
		if entry.SerialNumber == nil || entry.SerialNumber.Sign() <= 0 {
			return nil, fmt.Errorf("%w: revocation %d has invalid serial", ErrInvalidConfig, i)
		}
		key := revocationKey{
			issuer: string(entry.Issuer),
			serial: entry.SerialNumber.String(),
		}
		entry.TrustDomain = td
		entry.Issuer = append([]byte(nil), entry.Issuer...)
		entry.SerialNumber = new(big.Int).Set(entry.SerialNumber)
		out[key] = entry
	}
	return out, nil
}

func revocationMatches(entries map[revocationKey]Revocation, cert *x509.Certificate) bool {
	if cert == nil || cert.SerialNumber == nil {
		return false
	}
	key := revocationKey{
		issuer: string(cert.RawIssuer),
		serial: cert.SerialNumber.String(),
	}
	_, revoked := entries[key]
	return revoked
}
