package netsec

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var (
	// ErrNilCertificate is returned when a nil certificate is supplied.
	ErrNilCertificate = errors.New("netsec: nil certificate")
	// ErrEmptyPath is returned when no certificate is supplied.
	ErrEmptyPath = errors.New("netsec: empty certificate path")
)

type derCertificate struct {
	TBS                asn1.RawValue
	SignatureAlgorithm asn1.RawValue
	SignatureValue     asn1.BitString
}

type derTBSCertificate struct {
	Version               asn1.RawValue `asn1:"optional,tag:0"`
	SerialNumber          asn1.RawValue
	Signature             asn1.RawValue
	Issuer                asn1.RawValue
	Validity              asn1.RawValue
	Subject               asn1.RawValue
	SubjectPublicKey      asn1.RawValue
	IssuerUniqueID        asn1.RawValue `asn1:"optional,tag:1"`
	SubjectUniqueID       asn1.RawValue `asn1:"optional,tag:2"`
	CertificateExtensions asn1.RawValue `asn1:"optional,tag:3"`
}

type derExtension struct {
	ID       asn1.ObjectIdentifier
	Critical bool `asn1:"optional"`
	Value    []byte
}

// ExtensionOIDsFromDER extracts extension OIDs from a DER-encoded certificate
// without merging or interpreting extension values.
func ExtensionOIDsFromDER(der []byte) ([]asn1.ObjectIdentifier, error) {
	if len(der) == 0 {
		return nil, errors.New("netsec: empty DER certificate")
	}

	var cert derCertificate
	rest, err := asn1.Unmarshal(der, &cert)
	if err != nil {
		return nil, fmt.Errorf("netsec: parse certificate: %w", err)
	}
	if len(rest) != 0 {
		return nil, errors.New("netsec: trailing data after DER certificate")
	}

	var tbs derTBSCertificate
	rest, err = asn1.Unmarshal(cert.TBS.FullBytes, &tbs)
	if err != nil {
		return nil, fmt.Errorf("netsec: parse TBS certificate: %w", err)
	}
	if len(rest) != 0 {
		return nil, errors.New("netsec: trailing data after TBS certificate")
	}

	if tbs.CertificateExtensions.Class != asn1.ClassContextSpecific ||
		tbs.CertificateExtensions.Tag != 3 {
		return nil, nil
	}
	if !tbs.CertificateExtensions.IsCompound {
		return nil, errors.New("netsec: malformed certificate extensions")
	}

	var exts []derExtension
	rest, err = asn1.Unmarshal(tbs.CertificateExtensions.Bytes, &exts)
	if err != nil {
		return nil, fmt.Errorf("netsec: parse certificate extensions: %w", err)
	}
	if len(rest) != 0 {
		return nil, errors.New("netsec: trailing data after certificate extensions")
	}

	oids := make([]asn1.ObjectIdentifier, len(exts))
	for i, ext := range exts {
		oids[i] = ext.ID
	}
	return oids, nil
}

func checkOIDs(oids []asn1.ObjectIdentifier) error {
	exts := make([]pkix.Extension, len(oids))
	for i, oid := range oids {
		exts[i].Id = oid
	}
	return CheckExtensionOIDs(exts)
}

// CheckDuplicateExtensionsDER rejects duplicate extension OIDs in a
// DER-encoded certificate.
func CheckDuplicateExtensionsDER(der []byte) error {
	oids, err := ExtensionOIDsFromDER(der)
	if err != nil {
		return err
	}
	return checkOIDs(oids)
}

// CheckDuplicateExtensionsBytes accepts either PEM or DER input.
func CheckDuplicateExtensionsBytes(data []byte) error {
	block, rest := pem.Decode(data)
	if block != nil {
		count := 0
		for block != nil {
			if block.Type == "CERTIFICATE" {
				count++
				if err := CheckDuplicateExtensionsDER(block.Bytes); err != nil {
					return err
				}
			}
			block, rest = pem.Decode(rest)
		}
		if count == 0 {
			return errors.New("netsec: no PEM certificate found")
		}
		return nil
	}
	if len(data) == 0 {
		return errors.New("netsec: empty certificate")
	}

	for len(data) > 0 {
		var raw asn1.RawValue
		remaining, err := asn1.Unmarshal(data, &raw)
		if err != nil {
			return fmt.Errorf("netsec: parse certificate: %w", err)
		}
		if err := CheckDuplicateExtensionsDER(raw.FullBytes); err != nil {
			return err
		}
		data = remaining
	}
	return nil
}

// ParseCertificate parses a DER certificate after rejecting duplicate
// extension OIDs. Unique OIDs are passed through to crypto/x509 unchanged.
func ParseCertificate(der []byte) (*x509.Certificate, error) {
	if block, _ := pem.Decode(der); block != nil {
		if block.Type != "CERTIFICATE" {
			return nil, fmt.Errorf("netsec: unexpected PEM block type %q", block.Type)
		}
		der = block.Bytes
	}
	if oids, err := ExtensionOIDsFromDER(der); err == nil {
		if err := checkOIDs(oids); err != nil {
			return nil, err
		}
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, duplicateExtensionError(err)
	}
	if err := CheckCertificate(cert); err != nil {
		return nil, err
	}
	return cert, nil
}

func duplicateExtensionError(err error) error {
	const marker = `duplicate extension with OID "`
	message := err.Error()
	start := strings.Index(message, marker)
	if start < 0 {
		return err
	}
	rest := message[start+len(marker):]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return err
	}
	oid, ok := parseOID(rest[:end])
	if !ok {
		return err
	}
	return &DuplicateExtensionError{OID: oid, Count: 2}
}

func parseOID(value string) (asn1.ObjectIdentifier, bool) {
	parts := strings.Split(value, ".")
	oid := make(asn1.ObjectIdentifier, len(parts))
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return nil, false
		}
		oid[i] = n
	}
	return oid, len(oid) > 0
}

// ParseCertificates parses one or more concatenated DER certificates.
func ParseCertificates(der []byte) ([]*x509.Certificate, error) {
	if len(der) == 0 {
		return nil, ErrEmptyPath
	}

	var certs []*x509.Certificate
	for len(der) > 0 {
		var raw asn1.RawValue
		rest, err := asn1.Unmarshal(der, &raw)
		if err != nil {
			return nil, fmt.Errorf("netsec: parse certificate: %w", err)
		}
		cert, err := ParseCertificate(raw.FullBytes)
		if err != nil {
			return nil, err
		}
		certs = append(certs, cert)
		der = rest
	}
	return certs, nil
}

// ParsePEMCertificate parses the first CERTIFICATE block.
func ParsePEMCertificate(data []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("netsec: no PEM certificate found")
	}
	if block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("netsec: unexpected PEM block type %q", block.Type)
	}
	return ParseCertificate(block.Bytes)
}

// ParsePEMCertificates parses every CERTIFICATE block in data.
func ParsePEMCertificates(data []byte) ([]*x509.Certificate, error) {
	var certs []*x509.Certificate
	for len(data) > 0 {
		block, rest := pem.Decode(data)
		if block == nil {
			break
		}
		data = rest
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := ParseCertificate(block.Bytes)
		if err != nil {
			return nil, err
		}
		certs = append(certs, cert)
	}
	if len(certs) == 0 {
		return nil, errors.New("netsec: no PEM certificate found")
	}
	return certs, nil
}
