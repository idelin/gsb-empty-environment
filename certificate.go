package netsec

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/asn1"
	"encoding/pem"
	"errors"
	"fmt"
)

// ParseCertificate parses a single DER-encoded X.509 certificate while
// enforcing the named-curve policy.
//
// The SubjectPublicKeyInfo is inspected before crypto/x509 parses the
// certificate. This makes explicit ECParameters a direct, typed failure rather
// than relying on whether a particular parser happens to understand them.
func ParseCertificate(der []byte) (*x509.Certificate, error) {
	if err := ValidateCertificateDER(der); err != nil {
		return nil, err
	}

	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	if err := CheckCertificate(cert); err != nil {
		return nil, err
	}
	return cert, nil
}

// ParseCertificatePEM parses the first CERTIFICATE block in data.
func ParseCertificatePEM(data []byte) (*x509.Certificate, error) {
	for len(data) > 0 {
		block, rest := pem.Decode(data)
		if block == nil {
			break
		}
		data = rest
		if block.Type != "CERTIFICATE" {
			continue
		}
		return ParseCertificate(block.Bytes)
	}
	return nil, errors.New("netsec: no CERTIFICATE block found")
}

// ParseCertificates parses a sequence of DER-encoded certificates. Every
// certificate in the sequence is subject to the named-curve policy.
func ParseCertificates(ders [][]byte) ([]*x509.Certificate, error) {
	certs := make([]*x509.Certificate, 0, len(ders))
	for i, der := range ders {
		cert, err := ParseCertificate(der)
		if err != nil {
			return nil, fmt.Errorf("netsec: certificate %d: %w", i, err)
		}
		certs = append(certs, cert)
	}
	return certs, nil
}

// ParseCertificatesPEM parses all CERTIFICATE blocks in data.
func ParseCertificatesPEM(data []byte) ([]*x509.Certificate, error) {
	var certs []*x509.Certificate
	for {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			break
		}
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
		return nil, errors.New("netsec: no CERTIFICATE blocks found")
	}
	return certs, nil
}

// ValidateCertificate applies the policy to either DER bytes, a PEM blob, or a
// certificate that has already been parsed. It returns nil only when all EC
// keys use a supported named curve.
func ValidateCertificate(input any) error {
	switch v := input.(type) {
	case []byte:
		if bytes.Contains(v, []byte("-----BEGIN")) {
			_, err := ParseCertificatePEM(v)
			return err
		}
		return ValidateCertificateDER(v)
	case string:
		return ValidateCertificate([]byte(v))
	case *x509.Certificate:
		return CheckCertificate(v)
	case x509.Certificate:
		return CheckCertificate(&v)
	default:
		return fmt.Errorf("netsec: unsupported certificate input type %T", input)
	}
}

// CheckCertificate applies the named-curve policy to an already parsed
// certificate.
func CheckCertificate(cert *x509.Certificate) error {
	if cert == nil {
		return errors.New("netsec: nil certificate")
	}

	if len(cert.RawSubjectPublicKeyInfo) > 0 {
		if err := ValidateSubjectPublicKeyInfo(cert.RawSubjectPublicKeyInfo); err != nil {
			return err
		}
	}

	// Also validate the decoded key. This catches callers that construct an
	// x509.Certificate directly with a custom elliptic.Curve implementation.
	switch pub := cert.PublicKey.(type) {
	case *ecdsa.PublicKey:
		if pub == nil || pub.Curve == nil {
			return fmt.Errorf("%w: missing curve", ErrInvalidECPublicKey)
		}
		if !IsNamedCurve(pub.Curve) {
			return fmt.Errorf("%w: custom curve implementations are not accepted", ErrUnsupportedECCurve)
		}
		if pub.X == nil || pub.Y == nil || !pub.Curve.IsOnCurve(pub.X, pub.Y) {
			return fmt.Errorf("%w: point is not on curve", ErrInvalidECPublicKey)
		}
	}

	return nil
}

// ValidateCertificateDER inspects the SubjectPublicKeyInfo of a DER-encoded
// certificate and rejects explicit or non-named EC parameters.
func ValidateCertificateDER(der []byte) error {
	spki, err := subjectPublicKeyInfoFromCertificate(der)
	if err != nil {
		return err
	}
	return ValidateSubjectPublicKeyInfo(spki)
}

func subjectPublicKeyInfoFromCertificate(der []byte) ([]byte, error) {
	var cert struct {
		TBS                asn1.RawValue
		SignatureAlgorithm asn1.RawValue
		SignatureValue     asn1.BitString
	}
	rest, err := asn1.Unmarshal(der, &cert)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid certificate: %v", ErrMalformedCertificate, err)
	}
	if len(rest) != 0 {
		return nil, fmt.Errorf("%w: trailing data after certificate", ErrMalformedCertificate)
	}
	if cert.TBS.Class != asn1.ClassUniversal || cert.TBS.Tag != asn1.TagSequence {
		return nil, fmt.Errorf("%w: tbsCertificate is not a SEQUENCE", ErrMalformedCertificate)
	}

	fields, err := sequenceFields(cert.TBS.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid tbsCertificate: %v", ErrMalformedCertificate, err)
	}

	index := 0
	if len(fields) > 0 && fields[0].Class == asn1.ClassContextSpecific && fields[0].Tag == 0 {
		index = 1
	}

	// serialNumber, signature, issuer, validity, subject, subjectPublicKeyInfo
	if len(fields) < index+6 {
		return nil, fmt.Errorf("%w: tbsCertificate has too few fields", ErrMalformedCertificate)
	}
	spki := fields[index+5]
	if spki.Class != asn1.ClassUniversal || spki.Tag != asn1.TagSequence {
		return nil, fmt.Errorf("%w: subjectPublicKeyInfo is not a SEQUENCE", ErrMalformedCertificate)
	}
	return spki.FullBytes, nil
}

func sequenceFields(body []byte) ([]asn1.RawValue, error) {
	var fields []asn1.RawValue
	for len(body) > 0 {
		var field asn1.RawValue
		rest, err := asn1.Unmarshal(body, &field)
		if err != nil {
			return nil, err
		}
		if len(rest) >= len(body) {
			return nil, errors.New("ASN.1 decoder made no progress")
		}
		fields = append(fields, field)
		body = rest
	}
	return fields, nil
}

// VerifyCertificateChain verifies a leaf certificate against roots and
// intermediates, applying the named-curve policy to every certificate in the
// supplied chain before verification.
func VerifyCertificateChain(leaf *x509.Certificate, intermediates []*x509.Certificate, roots *x509.CertPool, opts x509.VerifyOptions) ([][]*x509.Certificate, error) {
	if err := CheckCertificate(leaf); err != nil {
		return nil, fmt.Errorf("netsec: leaf: %w", err)
	}

	opts.Roots = roots
	opts.Intermediates = x509.NewCertPool()
	for i, intermediate := range intermediates {
		if err := CheckCertificate(intermediate); err != nil {
			return nil, fmt.Errorf("netsec: intermediate %d: %w", i, err)
		}
		opts.Intermediates.AddCert(intermediate)
	}

	return leaf.Verify(opts)
}
