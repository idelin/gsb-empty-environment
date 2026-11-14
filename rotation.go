package netsec

import (
	"crypto/x509"
	"fmt"
	"time"
)

// CertificateID identifies a leaf certificate by its raw issuer name and serial.
type CertificateID struct {
	Issuer       []byte
	SerialNumber string
}

// CertificateIDForCertificate constructs a stable certificate identifier.
func CertificateIDForCertificate(cert *x509.Certificate) (CertificateID, error) {
	if cert == nil {
		return CertificateID{}, fmt.Errorf("%w: nil certificate", ErrNoCertificate)
	}
	if cert.SerialNumber == nil || cert.SerialNumber.Sign() <= 0 {
		return CertificateID{}, fmt.Errorf("%w: invalid certificate serial", ErrInvalidCertificate)
	}
	if len(cert.RawIssuer) == 0 {
		return CertificateID{}, fmt.Errorf("%w: certificate has no issuer", ErrInvalidCertificate)
	}
	return CertificateID{
		Issuer:       append([]byte(nil), cert.RawIssuer...),
		SerialNumber: cert.SerialNumber.String(),
	}, nil
}

// Matches reports whether the identifier names cert.
func (id CertificateID) Matches(cert *x509.Certificate) bool {
	if cert == nil || cert.SerialNumber == nil {
		return false
	}
	return id.SerialNumber == cert.SerialNumber.String() && string(id.Issuer) == string(cert.RawIssuer)
}

func (id CertificateID) validate() error {
	if id.SerialNumber == "" {
		return fmt.Errorf("%w: empty certificate serial", ErrInvalidConfig)
	}
	if len(id.Issuer) == 0 {
		return fmt.Errorf("%w: empty certificate issuer", ErrInvalidConfig)
	}
	return nil
}

// RotationRecord is a workload's active certificate set for one rotation
// generation. When a record exists, certificates outside Active are rejected
// even if they have not yet expired.
type RotationRecord struct {
	Workload   SPIFFEID
	Generation Generation
	Active     []CertificateID
	ValidUntil time.Time
}

type rotationState struct {
	generation  Generation
	initialized bool
	records     map[SPIFFEID]RotationRecord
}

func compileRotations(updateGen Generation, records []RotationRecord) (map[SPIFFEID]RotationRecord, error) {
	out := make(map[SPIFFEID]RotationRecord, len(records))
	for i, record := range records {
		if record.Workload.IsZero() {
			return nil, fmt.Errorf("%w: rotation %d has no workload", ErrInvalidConfig, i)
		}
		canonicalWorkload, err := ParseSPIFFEID(record.Workload.String())
		if err != nil {
			return nil, fmt.Errorf("%w: rotation %d: %v", ErrInvalidConfig, i, err)
		}
		record.Workload = canonicalWorkload
		if record.Generation == 0 {
			record.Generation = updateGen
		}
		if record.Generation > updateGen {
			return nil, fmt.Errorf("%w: rotation %d generation %d exceeds update generation %d", ErrInvalidConfig, i, record.Generation, updateGen)
		}
		if _, exists := out[record.Workload]; exists {
			return nil, fmt.Errorf("%w: duplicate rotation for %s", ErrInvalidConfig, record.Workload)
		}
		active := make([]CertificateID, 0, len(record.Active))
		for _, id := range record.Active {
			if err := id.validate(); err != nil {
				return nil, fmt.Errorf("%w: rotation %d: %v", ErrInvalidConfig, i, err)
			}
			id.Issuer = append([]byte(nil), id.Issuer...)
			active = append(active, id)
		}
		record.Active = active
		out[record.Workload] = record
	}
	return out, nil
}

func (record RotationRecord) verifyCertificate(now time.Time, cert *x509.Certificate) error {
	if !record.ValidUntil.IsZero() && now.After(record.ValidUntil) {
		return fmt.Errorf("%w: %s expired at %s", ErrRotationExpired, record.Workload, record.ValidUntil.UTC().Format(time.RFC3339))
	}
	for _, active := range record.Active {
		if active.Matches(cert) {
			return nil
		}
	}
	return fmt.Errorf("%w: %s generation %d", ErrRotated, record.Workload, record.Generation)
}
