package netsec

import (
	"bytes"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"fmt"
	"math/big"
)

type certificateEnvelope struct {
	TBS                asn1.RawValue
	SignatureAlgorithm asn1.RawValue
	SignatureValue     asn1.BitString
}

type tbsEnvelope struct {
	Version            int `asn1:"optional,explicit,default:0,tag:0"`
	SerialNumber       *big.Int
	SignatureAlgorithm asn1.RawValue
	Issuer             asn1.RawValue
	Validity           asn1.RawValue
	Subject            asn1.RawValue
	PublicKey          asn1.RawValue
}

type publicKeyInfoASN1 struct {
	Algorithm pkix.AlgorithmIdentifier
	PublicKey asn1.BitString
}

// rawCertificate keeps exactly the ASN.1 fields needed for path verification.
// Unlike crypto/x509 it accepts a DSA SubjectPublicKeyInfo whose parameters
// are absent, because those parameters may be inherited from the issuer.
type rawCertificate struct {
	der      []byte
	tbs      []byte
	issuer   []byte
	subject  []byte
	sigAlg   pkix.AlgorithmIdentifier
	sigValue []byte
	keyAlg   pkix.AlgorithmIdentifier
	keyBits  []byte
	parsed   *x509.Certificate
}

func parseRawCertificate(der []byte) (*rawCertificate, error) {
	var envelope certificateEnvelope
	rest, err := asn1.Unmarshal(der, &envelope)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidCertificate, err)
	}
	if len(rest) != 0 {
		return nil, fmt.Errorf("%w: trailing data", ErrInvalidCertificate)
	}
	if len(envelope.TBS.FullBytes) == 0 {
		return nil, fmt.Errorf("%w: missing tbsCertificate", ErrInvalidCertificate)
	}

	var tbs tbsEnvelope
	_, err = asn1.Unmarshal(envelope.TBS.FullBytes, &tbs)
	if err != nil {
		return nil, fmt.Errorf("%w: malformed tbsCertificate: %v", ErrInvalidCertificate, err)
	}
	if !bytes.Equal(tbs.SignatureAlgorithm.FullBytes, envelope.SignatureAlgorithm.FullBytes) {
		return nil, fmt.Errorf("%w: inner and outer signature algorithm identifiers differ", ErrInvalidCertificate)
	}

	var sigAlg pkix.AlgorithmIdentifier
	if _, err := asn1.Unmarshal(envelope.SignatureAlgorithm.FullBytes, &sigAlg); err != nil {
		return nil, fmt.Errorf("%w: malformed signature algorithm: %v", ErrInvalidCertificate, err)
	}

	var keyInfo publicKeyInfoASN1
	if _, err := asn1.Unmarshal(tbs.PublicKey.FullBytes, &keyInfo); err != nil {
		return nil, fmt.Errorf("%w: malformed subjectPublicKeyInfo: %v", ErrInvalidCertificate, err)
	}

	parsed, _ := x509.ParseCertificate(der)

	return &rawCertificate{
		der:      append([]byte(nil), der...),
		tbs:      append([]byte(nil), envelope.TBS.FullBytes...),
		issuer:   append([]byte(nil), tbs.Issuer.FullBytes...),
		subject:  append([]byte(nil), tbs.Subject.FullBytes...),
		sigAlg:   sigAlg,
		sigValue: append([]byte(nil), envelope.SignatureValue.RightAlign()...),
		keyAlg:   keyInfo.Algorithm,
		keyBits:  append([]byte(nil), keyInfo.PublicKey.RightAlign()...),
		parsed:   parsed,
	}, nil
}

func (c *rawCertificate) isSelfIssued() bool {
	return bytes.Equal(c.subject, c.issuer)
}
