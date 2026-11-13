package netsec

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
)

// ErrEmptyIdentity is returned when a certificate has both an empty Subject
// and no Subject Alternative Names. Such a certificate carries no identity
// information and must not match any hostname.
var ErrEmptyIdentity = errors.New("netsec: certificate has empty subject and no SANs; it cannot match any hostname")

// subjectEmpty reports whether the certificate Subject holds no attributes.
func subjectEmpty(subject pkix.Name) bool {
	return subject.CommonName == "" &&
		len(subject.Country) == 0 &&
		len(subject.Organization) == 0 &&
		len(subject.OrganizationalUnit) == 0 &&
		len(subject.Locality) == 0 &&
		len(subject.Province) == 0 &&
		len(subject.StreetAddress) == 0 &&
		len(subject.PostalCode) == 0 &&
		subject.SerialNumber == "" &&
		len(subject.Names) == 0 &&
		len(subject.ExtraNames) == 0
}

// hasSAN reports whether the certificate carries any SAN values.
func hasSAN(cert *x509.Certificate) bool {
	return len(cert.DNSNames) > 0 ||
		len(cert.EmailAddresses) > 0 ||
		len(cert.IPAddresses) > 0 ||
		len(cert.URIs) > 0
}

// VerifyHostname verifies that cert is valid for host. Unlike a bare
// x509.Certificate.VerifyHostname call, a certificate whose Subject is empty
// and which has no SANs is rejected outright instead of being treated as
// matching arbitrary names. A certificate with an empty Subject is still
// accepted when its SANs contain the target name.
func VerifyHostname(cert *x509.Certificate, host string) error {
	if cert == nil {
		return errors.New("netsec: nil certificate")
	}
	if subjectEmpty(cert.Subject) && !hasSAN(cert) {
		return fmt.Errorf("%w (host %q)", ErrEmptyIdentity, host)
	}
	return cert.VerifyHostname(host)
}
