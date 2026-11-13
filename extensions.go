package netsec

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"fmt"
)

// NameConstraintsOID is the dotted OID for the X.509 name constraints
// extension (RFC 5280, section 4.2.1.10).
const NameConstraintsOID = "2.5.29.30"

// OIDExtensionNameConstraints is the ASN.1 form of NameConstraintsOID.
var OIDExtensionNameConstraints = asn1.ObjectIdentifier{2, 5, 29, 30}

// OIDNameConstraints is an alias for OIDExtensionNameConstraints.
var OIDNameConstraints = OIDExtensionNameConstraints

// ErrDuplicateExtension is returned when a certificate contains more than one
// extension with the same OID. The duplicate values are never merged.
var ErrDuplicateExtension = errors.New("netsec: certificate contains duplicate extension OID")

// ErrDuplicateOID is an alias for ErrDuplicateExtension.
var ErrDuplicateOID = ErrDuplicateExtension

// DuplicateExtensionError identifies an extension OID that occurs more than
// once in a certificate.
type DuplicateExtensionError struct {
	OID   asn1.ObjectIdentifier
	Count int
}

func (e *DuplicateExtensionError) Error() string {
	return fmt.Sprintf("x509: certificate contains duplicate extension with OID %q", e.OID.String())
}

func (e *DuplicateExtensionError) Unwrap() error {
	return ErrDuplicateExtension
}

func (e *DuplicateExtensionError) Is(target error) bool {
	return target == ErrDuplicateExtension
}

// DuplicateOIDError is an alias for DuplicateExtensionError.
type DuplicateOIDError = DuplicateExtensionError

// DuplicateExtensionsError is an alias for DuplicateExtensionError.
type DuplicateExtensionsError = DuplicateExtensionError

// ExtensionOIDCounts returns the number of times each extension OID appears.
func ExtensionOIDCounts(exts []pkix.Extension) map[string]int {
	counts := make(map[string]int, len(exts))
	for _, ext := range exts {
		counts[ext.Id.String()]++
	}
	return counts
}

// DuplicateExtensionOIDs returns the duplicate OIDs in first-seen order.
func DuplicateExtensionOIDs(exts []pkix.Extension) []asn1.ObjectIdentifier {
	counts := ExtensionOIDCounts(exts)
	seen := make(map[string]bool, len(counts))
	var duplicates []asn1.ObjectIdentifier
	for _, ext := range exts {
		oid := ext.Id.String()
		if counts[oid] < 2 || seen[oid] {
			continue
		}
		seen[oid] = true
		duplicates = append(duplicates, ext.Id)
	}
	return duplicates
}

// DuplicateOIDs is an alias for DuplicateExtensionOIDs.
func DuplicateOIDs(exts []pkix.Extension) []asn1.ObjectIdentifier {
	return DuplicateExtensionOIDs(exts)
}

// CheckExtensionOIDs rejects any extension OID that occurs more than once.
// It deliberately does not combine duplicate extension values.
func CheckExtensionOIDs(exts []pkix.Extension) error {
	counts := ExtensionOIDCounts(exts)
	seen := make(map[string]bool, len(counts))
	for _, ext := range exts {
		oid := ext.Id.String()
		if counts[oid] < 2 || seen[oid] {
			continue
		}
		seen[oid] = true
		return &DuplicateExtensionError{OID: ext.Id, Count: counts[oid]}
	}
	return nil
}

// ValidateExtensions checks an extension slice or certificate for duplicate
// OIDs.
func ValidateExtensions(value any) error {
	switch exts := value.(type) {
	case []pkix.Extension:
		return CheckExtensionOIDs(exts)
	default:
		return CheckCertificate(value)
	}
}

// CheckExtensions is an alias for ValidateExtensions.
func CheckExtensions(value any) error {
	return ValidateExtensions(value)
}

// RejectDuplicateExtensions is an alias for CheckDuplicateExtensions.
func RejectDuplicateExtensions(value any) error {
	return CheckDuplicateExtensions(value)
}

// CheckForDuplicateExtensions is an alias for CheckDuplicateExtensions.
func CheckForDuplicateExtensions(value any) error {
	return CheckDuplicateExtensions(value)
}

// CheckDuplicateOIDs is an alias for CheckDuplicateExtensions.
func CheckDuplicateOIDs(value any) error {
	return CheckDuplicateExtensions(value)
}

// RejectDuplicateOIDs is an alias for CheckDuplicateExtensions.
func RejectDuplicateOIDs(value any) error {
	return CheckDuplicateExtensions(value)
}

// CheckDuplicateExtensions accepts an *x509.Certificate, x509.Certificate,
// []pkix.Extension, DER-encoded certificate, PEM-encoded certificate, or a
// string containing either encoding.
func CheckDuplicateExtensions(value any) error {
	switch v := value.(type) {
	case *x509.Certificate:
		if v == nil {
			return ErrNilCertificate
		}
		return CheckExtensionOIDs(v.Extensions)
	case x509.Certificate:
		return CheckExtensionOIDs(v.Extensions)
	case []pkix.Extension:
		return CheckExtensionOIDs(v)
	case []byte:
		return CheckDuplicateExtensionsBytes(v)
	case string:
		return CheckDuplicateExtensionsBytes([]byte(v))
	default:
		return fmt.Errorf("netsec: unsupported certificate value %T", value)
	}
}

// CheckCertificateExtensions is an alias for CheckCertificate.
func CheckCertificateExtensions(value any) error {
	return CheckCertificate(value)
}

// HasDuplicateExtensions reports whether value contains a repeated OID.
func HasDuplicateExtensions(value any) bool {
	switch v := value.(type) {
	case *x509.Certificate:
		return v != nil && CheckDuplicateExtensions(v) != nil
	case x509.Certificate, []pkix.Extension, []byte, string:
		return CheckDuplicateExtensions(value) != nil
	default:
		return false
	}
}

// HasDuplicateExtension is an alias for HasDuplicateExtensions.
func HasDuplicateExtension(value any) bool {
	return HasDuplicateExtensions(value)
}

// HasDuplicateOID is an alias for HasDuplicateExtensions.
func HasDuplicateOID(value any) bool {
	return HasDuplicateExtensions(value)
}

// CheckCertificate rejects duplicate extension OIDs in a certificate. It
// accepts an *x509.Certificate, x509.Certificate, DER/PEM data, or an
// extension slice.
func CheckCertificate(value any) error {
	switch v := value.(type) {
	case nil:
		return ErrNilCertificate
	case *x509.Certificate:
		return checkCertificateOIDs(v)
	case x509.Certificate:
		return checkCertificateOIDs(&v)
	default:
		return CheckDuplicateExtensions(value)
	}
}

func checkCertificateOIDs(cert *x509.Certificate) error {
	if cert == nil {
		return ErrNilCertificate
	}
	if err := CheckExtensionOIDs(cert.Extensions); err != nil {
		return err
	}
	if len(cert.Raw) > 0 {
		if oids, err := ExtensionOIDsFromDER(cert.Raw); err == nil {
			if err := checkOIDs(oids); err != nil {
				return err
			}
		}
	}
	return nil
}

// ValidateCertificate is an alias for CheckCertificate.
func ValidateCertificate(value any, options ...any) error {
	if len(options) > 0 {
		return ValidateCertificatePath(value, options...)
	}
	return CheckCertificate(value)
}

// CheckCertificatePath rejects duplicate extension OIDs in every certificate
// in the path. A certificate whose OIDs are all unique passes this check and
// remains eligible for full path validation.
func CheckCertificatePath(path any, options ...any) error {
	chain, err := pathCertificates(path)
	if err != nil {
		return err
	}
	if len(chain) == 0 {
		return ErrEmptyPath
	}
	for i, cert := range chain {
		if cert == nil {
			return fmt.Errorf("netsec: nil certificate in path at index %d", i)
		}
		if err := CheckCertificate(cert); err != nil {
			return fmt.Errorf("netsec: certificate at index %d: %w", i, err)
		}
	}
	return nil
}

// CheckPath is an alias for CheckCertificatePath.
func CheckPath(path any, options ...any) error {
	return CheckCertificatePath(path, options...)
}

// CheckChain is an alias for CheckCertificatePath.
func CheckChain(path any, options ...any) error {
	return CheckCertificatePath(path, options...)
}
