// Package netsec provides parsing and revocation lookup for X.509 CRLs.
//
// The package deliberately treats an indirect CRL as all-or-nothing: if an
// indirect CRL contains an entry whose certificate issuer cannot be
// determined from that entry's certificateIssuer extension, the CRL is
// rejected instead of inheriting the issuer from the preceding entry.
package netsec

import (
	"bytes"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"fmt"
	"math/big"
	"time"
)

var (
	// OIDCertificateIssuer identifies the CRL entry extension that names the
	// issuer of the certificate associated with an indirect CRL entry.
	OIDCertificateIssuer = asn1.ObjectIdentifier{2, 5, 29, 29}

	// OIDIssuingDistributionPoint identifies the CRL extension that declares
	// whether the CRL is indirect.
	OIDIssuingDistributionPoint = asn1.ObjectIdentifier{2, 5, 29, 28}
)

var (
	// ErrMissingCertificateIssuer is returned when an indirect CRL contains an
	// entry without a usable certificateIssuer extension. The whole CRL must
	// not be used in that case.
	ErrMissingCertificateIssuer = errors.New("netsec: indirect CRL entry has no certificateIssuer")

	// ErrIndirectCRLUnusable is an alias for ErrMissingCertificateIssuer.
	ErrIndirectCRLUnusable = ErrMissingCertificateIssuer

	// ErrMissingIssuer is an alias for ErrMissingCertificateIssuer.
	ErrMissingIssuer = ErrMissingCertificateIssuer

	// ErrInvalidCertificateIssuer is returned when a certificateIssuer
	// extension is malformed or does not contain a directoryName.
	ErrInvalidCertificateIssuer = errors.New("netsec: invalid certificateIssuer extension")

	// ErrMalformedCRL is returned when CRL or CRL extension encodings are
	// malformed.
	ErrMalformedCRL = errors.New("netsec: malformed CRL")

	// ErrNotIndirectCRL is returned by functions that require an indirect CRL.
	ErrNotIndirectCRL = errors.New("netsec: CRL is not indirect")

	// ErrIssuerMismatch is returned when a direct CRL is asked about a
	// certificate issued by a different CA.
	ErrIssuerMismatch = errors.New("netsec: certificate issuer does not match CRL issuer")
)

// GeneralName is one GeneralName value from a certificateIssuer extension.
//
// Raw contains the complete DER encoding of the GeneralName. For a
// directoryName, RawDirectoryName contains the DER encoding of the Name
// (a SEQUENCE), which can be compared directly with
// x509.Certificate.RawIssuer.
type GeneralName struct {
	Class            int
	Tag              int
	Raw              []byte
	RawDirectoryName []byte
	DirectoryName    *pkix.Name
}

// Entry is a revoked-certificate entry with its resolved certificate issuer.
//
// x509.RevocationListEntry is embedded so callers can use the standard
// library fields (SerialNumber, RevocationTime, ReasonCode, Extensions)
// directly.
type Entry struct {
	x509.RevocationListEntry

	// Issuer is the certificate issuer resolved for this entry. For an
	// indirect CRL this always comes from certificateIssuer; for a direct CRL
	// it is the CRL issuer.
	Issuer pkix.Name

	// CertificateIssuer is the same issuer as Issuer. It is a pointer so a
	// caller can distinguish an explicitly resolved issuer from the zero
	// value.
	CertificateIssuer *pkix.Name

	// RawIssuer is the DER-encoded Name of the certificate issuer.
	RawIssuer []byte

	// HasCertificateIssuer reports whether the entry carried a
	// certificateIssuer extension.
	HasCertificateIssuer bool

	// IssuerNames contains every GeneralName from certificateIssuer.
	IssuerNames []GeneralName
}

// RevocationList is a parsed CRL together with issuer information resolved for
// every entry.
type RevocationList struct {
	*x509.RevocationList

	// Indirect reports whether the issuingDistributionPoint extension has
	// indirectCRL set.
	Indirect bool

	// IsIndirectCRL is an alias for Indirect.
	IsIndirectCRL bool

	// IndirectCRL is an alias for Indirect.
	IndirectCRL bool

	// Entries contains all revoked-certificate entries in CRL order.
	Entries []Entry
}

// CRL is an alias for RevocationList.
type CRL = RevocationList

// RevocationEntry is an alias for Entry.
type RevocationEntry = Entry

// ParseCRL parses a DER-encoded CRL. It accepts either the DER bytes or an
// already parsed *x509.RevocationList.
//
// If the CRL is indirect, every entry must carry a certificateIssuer extension
// containing a directoryName. If any entry cannot be resolved, ParseCRL
// returns an error and the CRL must not be used.
func ParseCRL(input any) (*RevocationList, error) {
	rl, err := revocationListFrom(input)
	if err != nil {
		return nil, err
	}
	return fromX509RevocationList(rl)
}

// ParseRevocationList is an alias for ParseCRL.
func ParseRevocationList(input any) (*RevocationList, error) {
	return ParseCRL(input)
}

// ParseIndirectCRL is an alias for ParseCRL. It does not additionally require
// the CRL to be indirect; use ValidateIndirectCRL for that.
func ParseIndirectCRL(input any) (*RevocationList, error) {
	return ParseCRL(input)
}

// NewCRL is an alias for ParseCRL.
func NewCRL(input any) (*RevocationList, error) {
	return ParseCRL(input)
}

// FromX509RevocationList resolves issuer information for an already parsed CRL.
func FromX509RevocationList(rl *x509.RevocationList) (*RevocationList, error) {
	if rl == nil {
		return nil, fmt.Errorf("%w: nil revocation list", ErrMalformedCRL)
	}
	return fromX509RevocationList(rl)
}

// NewIndirectCRL is an alias for FromX509RevocationList.
func NewIndirectCRL(rl *x509.RevocationList) (*RevocationList, error) {
	return FromX509RevocationList(rl)
}

// ValidateCRL parses a DER CRL or validates an already parsed CRL without
// requiring it to be indirect. A nil return means every entry's certificate
// issuer was determined successfully.
func ValidateCRL(input any) error {
	_, err := ParseCRL(input)
	return err
}

// CheckCRL is an alias for ValidateCRL.
func CheckCRL(input any) error {
	return ValidateCRL(input)
}

// ValidateIndirectCRL validates an indirect CRL. It returns ErrNotIndirectCRL
// if input is a well-formed direct CRL.
func ValidateIndirectCRL(input any) error {
	rl, err := ParseCRL(input)
	if err != nil {
		return err
	}
	if !rl.Indirect {
		return ErrNotIndirectCRL
	}
	return nil
}

// CheckIndirectCRL is an alias for ValidateIndirectCRL.
func CheckIndirectCRL(input any) error {
	return ValidateIndirectCRL(input)
}

func revocationListFrom(input any) (*x509.RevocationList, error) {
	switch value := input.(type) {
	case nil:
		return nil, fmt.Errorf("%w: nil input", ErrMalformedCRL)
	case []byte:
		rl, err := x509.ParseRevocationList(value)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrMalformedCRL, err)
		}
		return rl, nil
	case *x509.RevocationList:
		if value == nil {
			return nil, fmt.Errorf("%w: nil revocation list", ErrMalformedCRL)
		}
		return value, nil
	case *RevocationList:
		if value == nil || value.RevocationList == nil {
			return nil, fmt.Errorf("%w: nil revocation list", ErrMalformedCRL)
		}
		return value.RevocationList, nil
	default:
		return nil, fmt.Errorf("%w: unsupported input type %T", ErrMalformedCRL, input)
	}
}

func fromX509RevocationList(rl *x509.RevocationList) (*RevocationList, error) {
	indirect, err := isIndirectCRL(rl.Extensions)
	if err != nil {
		return nil, err
	}
	if !indirect {
		// certificateIssuer is only valid in an indirect CRL. If the
		// issuingDistributionPoint extension is missing but entries carry the
		// extension, treat the CRL as indirect so the same all-or-nothing
		// issuer checks apply.
		for _, revoked := range revokedEntriesOf(rl) {
			if entryHasCertificateIssuer(revoked.Extensions) {
				indirect = true
				break
			}
		}
	}

	out := &RevocationList{
		RevocationList: rl,
		Indirect:       indirect,
		IsIndirectCRL:  indirect,
		IndirectCRL:    indirect,
		Entries:        make([]Entry, 0),
	}

	for index, revoked := range revokedEntriesOf(rl) {
		entry, err := resolveEntry(rl, revoked, indirect, index)
		if err != nil {
			return nil, err
		}
		out.Entries = append(out.Entries, entry)
	}
	return out, nil
}

func entryHasCertificateIssuer(extensions []pkix.Extension) bool {
	for _, ext := range extensions {
		if ext.Id.Equal(OIDCertificateIssuer) {
			return true
		}
	}
	return false
}

func revokedEntriesOf(rl *x509.RevocationList) []x509.RevocationListEntry {
	if len(rl.RevokedCertificateEntries) > 0 {
		return rl.RevokedCertificateEntries
	}
	if len(rl.RevokedCertificates) == 0 {
		return nil
	}

	entries := make([]x509.RevocationListEntry, len(rl.RevokedCertificates))
	for index, revoked := range rl.RevokedCertificates {
		entries[index] = x509.RevocationListEntry{
			SerialNumber:   revoked.SerialNumber,
			RevocationTime: revoked.RevocationTime,
			Extensions:     revoked.Extensions,
		}
	}
	return entries
}

func resolveEntry(crl *x509.RevocationList, revoked x509.RevocationListEntry, indirect bool, index int) (Entry, error) {
	entry := Entry{RevocationListEntry: revoked}

	if !indirect {
		entry.RawIssuer = append([]byte(nil), crl.RawIssuer...)
		entry.Issuer = crl.Issuer
		issuer := crl.Issuer
		entry.CertificateIssuer = &issuer
		return entry, nil
	}

	var issuerExtensions []pkix.Extension
	for _, ext := range revoked.Extensions {
		if ext.Id.Equal(OIDCertificateIssuer) {
			issuerExtensions = append(issuerExtensions, ext)
		}
	}
	if len(issuerExtensions) == 0 {
		return Entry{}, fmt.Errorf("%w: entry %d (serial %v)", ErrMissingCertificateIssuer, index, revoked.SerialNumber)
	}
	if len(issuerExtensions) > 1 {
		return Entry{}, fmt.Errorf("%w: entry %d has duplicate certificateIssuer extensions", ErrInvalidCertificateIssuer, index)
	}

	names, err := parseGeneralNames(issuerExtensions[0].Value)
	if err != nil {
		return Entry{}, fmt.Errorf("%w: entry %d: %v", ErrInvalidCertificateIssuer, index, err)
	}

	var directoryNames []GeneralName
	for _, name := range names {
		if name.RawDirectoryName != nil {
			directoryNames = append(directoryNames, name)
		}
	}
	if len(directoryNames) == 0 {
		return Entry{}, fmt.Errorf("%w: entry %d has no directoryName", ErrMissingCertificateIssuer, index)
	}
	for _, name := range directoryNames[1:] {
		if !issuerEqual(directoryNames[0].RawDirectoryName, name.RawDirectoryName) {
			return Entry{}, fmt.Errorf("%w: entry %d has ambiguous directoryName values", ErrInvalidCertificateIssuer, index)
		}
	}

	// All directoryName values in one certificateIssuer extension should name
	// the same issuer. Keep the first one as the canonical value and let
	// lookup consider every name.
	entry.IssuerNames = names
	entry.HasCertificateIssuer = true
	entry.RawIssuer = append([]byte(nil), directoryNames[0].RawDirectoryName...)
	entry.Issuer = *directoryNames[0].DirectoryName
	issuer := entry.Issuer
	entry.CertificateIssuer = &issuer
	return entry, nil
}

func isIndirectCRL(extensions []pkix.Extension) (bool, error) {
	var idpExtensions []pkix.Extension
	for _, ext := range extensions {
		if ext.Id.Equal(OIDIssuingDistributionPoint) {
			idpExtensions = append(idpExtensions, ext)
		}
	}
	if len(idpExtensions) == 0 {
		return false, nil
	}
	if len(idpExtensions) > 1 {
		return false, fmt.Errorf("%w: duplicate issuingDistributionPoint extensions", ErrMalformedCRL)
	}

	indirect, err := parseIssuingDistributionPoint(idpExtensions[0].Value)
	if err != nil {
		return false, fmt.Errorf("%w: %v", ErrMalformedCRL, err)
	}
	return indirect, nil
}

func parseIssuingDistributionPoint(der []byte) (bool, error) {
	var sequence asn1.RawValue
	rest, err := asn1.Unmarshal(der, &sequence)
	if err != nil {
		return false, err
	}
	if len(rest) != 0 {
		return false, errors.New("trailing data after issuingDistributionPoint")
	}
	if sequence.Class != asn1.ClassUniversal || sequence.Tag != asn1.TagSequence || !sequence.IsCompound {
		return false, errors.New("issuingDistributionPoint is not a SEQUENCE")
	}

	remaining := sequence.Bytes
	for len(remaining) > 0 {
		var field asn1.RawValue
		rest, err := asn1.Unmarshal(remaining, &field)
		if err != nil {
			return false, err
		}
		remaining = rest
		if field.Class != asn1.ClassContextSpecific || field.Tag != 4 {
			continue
		}

		value := field.Bytes
		if field.IsCompound {
			var nested asn1.RawValue
			nestedRest, err := asn1.Unmarshal(value, &nested)
			if err != nil {
				return false, err
			}
			if len(nestedRest) != 0 {
				return false, errors.New("trailing data in indirectCRL BOOLEAN")
			}
			if nested.Class != asn1.ClassUniversal || nested.Tag != asn1.TagBoolean {
				return false, errors.New("indirectCRL is not a BOOLEAN")
			}
			value = nested.Bytes
		}
		if len(value) == 0 {
			return false, errors.New("empty indirectCRL BOOLEAN")
		}
		for _, octet := range value {
			if octet != 0 {
				return true, nil
			}
		}
		return false, nil
	}
	return false, nil
}

// parseGeneralNames parses a DER-encoded GeneralNames value.
func parseGeneralNames(der []byte) ([]GeneralName, error) {
	var sequence asn1.RawValue
	rest, err := asn1.Unmarshal(der, &sequence)
	if err != nil {
		return nil, err
	}
	if len(rest) != 0 {
		return nil, errors.New("trailing data after GeneralNames")
	}
	if sequence.Class != asn1.ClassUniversal || sequence.Tag != asn1.TagSequence || !sequence.IsCompound {
		return nil, errors.New("GeneralNames is not a SEQUENCE")
	}

	remaining := sequence.Bytes
	if len(remaining) == 0 {
		return nil, errors.New("GeneralNames is empty")
	}

	var names []GeneralName
	for len(remaining) > 0 {
		var raw asn1.RawValue
		rest, err := asn1.Unmarshal(remaining, &raw)
		if err != nil {
			return nil, err
		}
		remaining = rest

		name := GeneralName{
			Class: raw.Class,
			Tag:   raw.Tag,
			Raw:   append([]byte(nil), raw.FullBytes...),
		}
		if raw.Class == asn1.ClassContextSpecific && raw.Tag == 4 {
			rawName, parsedName, err := parseDirectoryName(raw)
			if err != nil {
				return nil, err
			}
			name.RawDirectoryName = rawName
			name.DirectoryName = &parsedName
		}
		names = append(names, name)
	}
	return names, nil
}

func parseDirectoryName(raw asn1.RawValue) ([]byte, pkix.Name, error) {
	var nameDER []byte

	// directoryName is a CHOICE-typed field and is normally encoded
	// explicitly: [4] { Name }. Some encoders use implicit tagging, so accept
	// the inner SEQUENCE directly as well.
	var inner asn1.RawValue
	rest, err := asn1.Unmarshal(raw.Bytes, &inner)
	if err == nil && len(rest) == 0 &&
		inner.Class == asn1.ClassUniversal && inner.Tag == asn1.TagSequence && inner.IsCompound {
		nameDER = append([]byte(nil), inner.FullBytes...)
	} else {
		nameDER, err = marshalSequence(raw.Bytes)
		if err != nil {
			return nil, pkix.Name{}, fmt.Errorf("malformed directoryName: %v", err)
		}
	}

	var rdns pkix.RDNSequence
	rest, err = asn1.Unmarshal(nameDER, &rdns)
	if err != nil {
		return nil, pkix.Name{}, fmt.Errorf("malformed directoryName: %v", err)
	}
	if len(rest) != 0 {
		return nil, pkix.Name{}, errors.New("trailing data after directoryName")
	}

	var name pkix.Name
	name.FillFromRDNSequence(&rdns)
	return nameDER, name, nil
}

func marshalSequence(contents []byte) ([]byte, error) {
	return asn1.Marshal(asn1.RawValue{
		Class:      asn1.ClassUniversal,
		Tag:        asn1.TagSequence,
		IsCompound: true,
		Bytes:      contents,
	})
}

// IsIndirect reports whether the CRL carries the indirectCRL
// issuingDistributionPoint indicator.
func (rl *RevocationList) IsIndirect() bool {
	return rl != nil && rl.Indirect
}

// X509 returns the underlying standard-library CRL.
func (rl *RevocationList) X509() *x509.RevocationList {
	if rl == nil {
		return nil
	}
	return rl.RevocationList
}

// Lookup returns the revoked entry for cert, matching both serial number and
// certificate issuer. A nil entry with a nil error means that this CRL has no
// entry for the certificate.
//
// For a direct CRL, a certificate from another issuer produces
// ErrIssuerMismatch rather than being treated as "not revoked".
func (rl *RevocationList) Lookup(cert *x509.Certificate) (*Entry, error) {
	if rl == nil || rl.RevocationList == nil {
		return nil, fmt.Errorf("%w: nil revocation list", ErrMalformedCRL)
	}
	if cert == nil {
		return nil, errors.New("netsec: nil certificate")
	}
	if cert.SerialNumber == nil {
		return nil, errors.New("netsec: certificate has nil serial number")
	}
	if !rl.Indirect && !issuerEqual(rl.RawIssuer, cert.RawIssuer) {
		return nil, ErrIssuerMismatch
	}

	for index := range rl.Entries {
		entry := &rl.Entries[index]
		if entry.SerialNumber == nil || entry.SerialNumber.Cmp(cert.SerialNumber) != 0 {
			continue
		}
		if entryMatchesIssuer(entry, cert.RawIssuer) {
			return entry, nil
		}
	}
	return nil, nil
}

// IsRevoked reports whether cert appears in the CRL. Lookup errors, such as an
// issuer mismatch on a direct CRL, are returned to the caller.
func (rl *RevocationList) IsRevoked(cert *x509.Certificate) (bool, error) {
	entry, err := rl.Lookup(cert)
	if err != nil {
		return false, err
	}
	return entry != nil, nil
}

// Revoked is an alias for IsRevoked.
func (rl *RevocationList) Revoked(cert *x509.Certificate) (bool, error) {
	return rl.IsRevoked(cert)
}

// CheckCertificate is an alias for IsRevoked.
func (rl *RevocationList) CheckCertificate(cert *x509.Certificate) (bool, error) {
	return rl.IsRevoked(cert)
}

// IsCertificateRevoked is an alias for IsRevoked.
func (rl *RevocationList) IsCertificateRevoked(cert *x509.Certificate) (bool, error) {
	return rl.IsRevoked(cert)
}

// Check is an alias for IsRevoked.
func (rl *RevocationList) Check(cert *x509.Certificate) (bool, error) {
	return rl.IsRevoked(cert)
}

// Lookup parses or validates a CRL and then looks up cert. It is a
// convenience wrapper for path-building code.
func Lookup(input any, cert *x509.Certificate) (*Entry, error) {
	rl, err := ParseCRL(input)
	if err != nil {
		return nil, err
	}
	return rl.Lookup(cert)
}

// IsRevoked parses or validates a CRL and reports whether cert is revoked.
func IsRevoked(input any, cert *x509.Certificate) (bool, error) {
	rl, err := ParseCRL(input)
	if err != nil {
		return false, err
	}
	return rl.IsRevoked(cert)
}

// CheckRevocation is an alias for IsRevoked.
func CheckRevocation(input any, cert *x509.Certificate) (bool, error) {
	return IsRevoked(input, cert)
}

// LookupSerialIssuer looks up an entry by serial number and DER-encoded issuer
// Name. A nil entry with a nil error means that no such entry exists.
func (rl *RevocationList) LookupSerialIssuer(serial *big.Int, rawIssuer []byte) (*Entry, error) {
	if rl == nil {
		return nil, fmt.Errorf("%w: nil revocation list", ErrMalformedCRL)
	}
	if serial == nil {
		return nil, errors.New("netsec: nil serial number")
	}

	for index := range rl.Entries {
		entry := &rl.Entries[index]
		if entry.SerialNumber == nil || entry.SerialNumber.Cmp(serial) != 0 {
			continue
		}
		if entryMatchesIssuer(entry, rawIssuer) {
			return entry, nil
		}
	}
	return nil, nil
}

// VerifySignature checks the CRL signature using issuer's public key. It does
// not build or validate a certification path for issuer.
func (rl *RevocationList) VerifySignature(issuer *x509.Certificate) error {
	if rl == nil || rl.RevocationList == nil {
		return fmt.Errorf("%w: nil revocation list", ErrMalformedCRL)
	}
	if issuer == nil {
		return errors.New("netsec: nil issuer certificate")
	}
	return issuer.CheckSignature(rl.SignatureAlgorithm, rl.RawTBSRevocationList, rl.Signature)
}

// RevocationTime returns the revocation date for cert if it is revoked.
func (rl *RevocationList) RevocationTime(cert *x509.Certificate) (time.Time, bool, error) {
	entry, err := rl.Lookup(cert)
	if err != nil {
		return time.Time{}, false, err
	}
	if entry == nil {
		return time.Time{}, false, nil
	}
	return entry.RevocationTime, true, nil
}

func entryMatchesIssuer(entry *Entry, rawIssuer []byte) bool {
	if entry == nil {
		return false
	}
	if issuerEqual(entry.RawIssuer, rawIssuer) {
		return true
	}
	for _, name := range entry.IssuerNames {
		if name.RawDirectoryName != nil && issuerEqual(name.RawDirectoryName, rawIssuer) {
			return true
		}
	}
	return false
}

func issuerEqual(a, b []byte) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	if bytes.Equal(a, b) {
		return true
	}

	var left, right pkix.RDNSequence
	leftRest, leftErr := asn1.Unmarshal(a, &left)
	rightRest, rightErr := asn1.Unmarshal(b, &right)
	if leftErr != nil || rightErr != nil || len(leftRest) != 0 || len(rightRest) != 0 {
		return false
	}

	var leftName, rightName pkix.Name
	leftName.FillFromRDNSequence(&left)
	rightName.FillFromRDNSequence(&right)
	return namesEqual(leftName, rightName)
}

func namesEqual(a, b pkix.Name) bool {
	if a.SerialNumber != b.SerialNumber || a.CommonName != b.CommonName {
		return false
	}
	if !stringsEqual(a.Country, b.Country) ||
		!stringsEqual(a.Organization, b.Organization) ||
		!stringsEqual(a.OrganizationalUnit, b.OrganizationalUnit) ||
		!stringsEqual(a.Locality, b.Locality) ||
		!stringsEqual(a.Province, b.Province) ||
		!stringsEqual(a.StreetAddress, b.StreetAddress) ||
		!stringsEqual(a.PostalCode, b.PostalCode) {
		return false
	}
	if len(a.Names) != len(b.Names) {
		return false
	}
	for index := range a.Names {
		if !a.Names[index].Type.Equal(b.Names[index].Type) {
			return false
		}
		if fmt.Sprint(a.Names[index].Value) != fmt.Sprint(b.Names[index].Value) {
			return false
		}
	}
	return true
}

func stringsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for index := range a {
		if a[index] != b[index] {
			return false
		}
	}
	return true
}
