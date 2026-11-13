package netsec

import (
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"fmt"
)

type asn1PolicyMapping struct {
	IssuerDomainPolicy  asn1.ObjectIdentifier
	SubjectDomainPolicy asn1.ObjectIdentifier
}

// DecodePolicyMappingsExtension decodes the DER contents of a
// policyMappings extension without rejecting anyPolicy entries. Use
// ParsePolicyMappingsExtension or ValidatePolicyMappings when validation is
// required.
func DecodePolicyMappingsExtension(der []byte) ([]PolicyMapping, error) {
	if len(der) == 0 {
		return nil, &InvalidPolicyMappingError{Reason: "empty policyMappings extension"}
	}

	var raw []asn1PolicyMapping
	rest, err := asn1.Unmarshal(der, &raw)
	if err != nil {
		return nil, fmt.Errorf("%w: cannot decode policyMappings extension: %v", ErrInvalidPolicyMapping, err)
	}
	if len(rest) != 0 {
		return nil, fmt.Errorf("%w: trailing data after policyMappings extension", ErrInvalidPolicyMapping)
	}

	out := make([]PolicyMapping, 0, len(raw))
	for _, entry := range raw {
		if err := validateOID(entry.IssuerDomainPolicy); err != nil {
			return nil, fmt.Errorf("%w: invalid issuerDomainPolicy", ErrInvalidPolicyMapping)
		}
		if err := validateOID(entry.SubjectDomainPolicy); err != nil {
			return nil, fmt.Errorf("%w: invalid subjectDomainPolicy", ErrInvalidPolicyMapping)
		}
		out = append(out, PolicyMapping{
			IssuerDomainPolicy:  cloneOID(entry.IssuerDomainPolicy),
			SubjectDomainPolicy: cloneOID(entry.SubjectDomainPolicy),
		})
	}
	return out, nil
}

// ParsePolicyMappingsExtension decodes and validates a policyMappings
// extension. A mapping from or to anyPolicy causes an error wrapping
// ErrAnyPolicyMapping. Mappings between two concrete policy OIDs are
// returned unchanged.
func ParsePolicyMappingsExtension(der []byte) ([]PolicyMapping, error) {
	mappings, err := DecodePolicyMappingsExtension(der)
	if err != nil {
		return nil, err
	}
	if err := ValidatePolicyMappings(mappings); err != nil {
		return nil, err
	}
	return mappings, nil
}

// MarshalPolicyMappingsExtension validates mappings and returns the DER
// contents of a policyMappings extension.
func MarshalPolicyMappingsExtension(mappings any) ([]byte, error) {
	normalized, err := NormalizePolicyMappings(mappings)
	if err != nil {
		return nil, err
	}
	raw := make([]asn1PolicyMapping, len(normalized))
	for i, m := range normalized {
		raw[i] = asn1PolicyMapping{
			IssuerDomainPolicy:  cloneOID(m.IssuerDomainPolicy),
			SubjectDomainPolicy: cloneOID(m.SubjectDomainPolicy),
		}
	}
	der, err := asn1.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("netsec: cannot encode policyMappings extension: %w", err)
	}
	return der, nil
}

// PolicyMappingsFromCertificate returns the policy mappings parsed by
// crypto/x509. It does not validate them.
func PolicyMappingsFromCertificate(cert *x509.Certificate) []PolicyMapping {
	if cert == nil {
		return nil
	}
	out := make([]PolicyMapping, 0, len(cert.PolicyMappings))
	for _, mapping := range cert.PolicyMappings {
		issuer, err := ParseOID(mapping.IssuerDomainPolicy.String())
		if err != nil {
			continue
		}
		subject, err := ParseOID(mapping.SubjectDomainPolicy.String())
		if err != nil {
			continue
		}
		out = append(out, PolicyMapping{
			IssuerDomainPolicy:  issuer,
			SubjectDomainPolicy: subject,
		})
	}
	return out
}

// ValidateCertificatePolicyMappings validates every policyMappings entry in
// cert, including entries in the raw policyMappings extension. It is safe to
// call as a hook from certification-path validation: a certificate whose
// mapping uses anyPolicy at either end is rejected.
func ValidateCertificatePolicyMappings(cert *x509.Certificate) error {
	if cert == nil {
		return ErrNilCertificate
	}

	if err := ValidatePolicyMappings(cert.PolicyMappings); err != nil {
		return err
	}

	for _, ext := range cert.Extensions {
		if ext.Id.Equal(PolicyMappingsOID) {
			if _, err := ParsePolicyMappingsExtension(ext.Value); err != nil {
				return fmt.Errorf("netsec: certificate policyMappings extension: %w", err)
			}
		}
	}
	return nil
}

// CheckCertificatePolicyMappings is an alias for
// ValidateCertificatePolicyMappings.
func CheckCertificatePolicyMappings(cert *x509.Certificate) error {
	return ValidateCertificatePolicyMappings(cert)
}

// PathPolicyMappingError reports a policy mapping failure in a certificate
// path. Index is the zero-based position of the certificate in the chain.
type PathPolicyMappingError struct {
	Index int
	Err   error
}

func (e *PathPolicyMappingError) Error() string {
	return fmt.Sprintf("netsec: certificate %d: %v", e.Index, e.Err)
}

func (e *PathPolicyMappingError) Unwrap() error { return e.Err }

// ValidatePathPolicyMappings validates the policyMappings extension of every
// certificate in a path. Concrete-to-concrete mappings are retained and
// accepted; any anyPolicy endpoint rejects the path.
func ValidatePathPolicyMappings(chain []*x509.Certificate) error {
	for i, cert := range chain {
		if err := ValidateCertificatePolicyMappings(cert); err != nil {
			return &PathPolicyMappingError{Index: i, Err: err}
		}
	}
	return nil
}

// ValidateCertificatePathPolicyMappings is an alias for
// ValidatePathPolicyMappings.
func ValidateCertificatePathPolicyMappings(chain []*x509.Certificate) error {
	return ValidatePathPolicyMappings(chain)
}

// CheckPathPolicyMappings is an alias for ValidatePathPolicyMappings.
func CheckPathPolicyMappings(chain []*x509.Certificate) error {
	return ValidatePathPolicyMappings(chain)
}

// ValidatePolicyMappingsExtensionBytes is an alias for
// ParsePolicyMappingsExtension.
func ValidatePolicyMappingsExtensionBytes(der []byte) error {
	_, err := ParsePolicyMappingsExtension(der)
	return err
}

// IsAnyPolicyMappingError reports whether err indicates that a policy
// mapping used anyPolicy.
func IsAnyPolicyMappingError(err error) bool {
	return errors.Is(err, ErrAnyPolicyMapping)
}
