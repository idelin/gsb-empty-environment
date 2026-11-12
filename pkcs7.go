package netsec

import (
	"crypto"
	"crypto/rsa"
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"fmt"
	"math/big"
)

// OIDs used by detached PKCS#7/CMS signatures (RFC 5652, RFC 8017).
var (
	oidPKCS7Data       = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 1}
	oidPKCS7SignedData = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}

	oidDigestSHA256     = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1}
	oidSigRSAEncryption = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 1}
	oidSigRSASHA256     = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 11}

	oidAttrContentType   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 3}
	oidAttrMessageDigest = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 4}
)

type algorithmIdentifier struct {
	Algorithm  asn1.ObjectIdentifier
	Parameters asn1.RawValue `asn1:"optional"`
}

type pkcs7ContentInfo struct {
	ContentType asn1.ObjectIdentifier
	Content     asn1.RawValue `asn1:"tag:0,explicit,optional"`
}

type pkcs7SignedData struct {
	Version                 int
	DigestAlgorithms        []algorithmIdentifier
	EncapsulatedContentInfo pkcs7EncapsulatedContent
	Certificates            asn1.RawValue `asn1:"tag:0,optional"`
	CRLs                    asn1.RawValue `asn1:"tag:1,optional"`
	SignerInfos             []pkcs7SignerInfo
}

type pkcs7EncapsulatedContent struct {
	ContentType asn1.ObjectIdentifier
}

type pkcs7IssuerAndSerial struct {
	Issuer       asn1.RawValue
	SerialNumber asn1.RawValue
}

type pkcs7SignerInfo struct {
	Version                   int
	IssuerAndSerialNumber     pkcs7IssuerAndSerial
	DigestAlgorithm           algorithmIdentifier
	AuthenticatedAttributes   []pkcs7Attribute `asn1:"tag:0,optional"`
	DigestEncryptionAlgorithm algorithmIdentifier
	EncryptedDigest           []byte
	UnauthenticatedAttributes []pkcs7Attribute `asn1:"tag:1,optional"`
}

type pkcs7Attribute struct {
	Type  asn1.ObjectIdentifier
	Value asn1.RawValue
}

type verifiedSignature struct {
	Signer    *x509.Certificate
	CertChain []*x509.Certificate
}

// verifyPKCS7Detached parses a DER-encoded detached PKCS#7 SignedData blob and
// verifies it against content. Only RSA-SHA256 signatures are accepted; every
// other digest or signature algorithm is rejected.
func verifyPKCS7Detached(der, content []byte) (*verifiedSignature, error) {
	var contentInfo pkcs7ContentInfo
	rest, err := asn1.Unmarshal(der, &contentInfo)
	if err != nil || len(rest) != 0 {
		return nil, wrap(ErrMalformedSignature, err)
	}
	if !contentInfo.ContentType.Equal(oidPKCS7SignedData) {
		return nil, fmt.Errorf("%w: content type is not signedData", ErrMalformedSignature)
	}
	if len(contentInfo.Content.Bytes) == 0 {
		return nil, fmt.Errorf("%w: missing signedData payload", ErrMalformedSignature)
	}

	var signedData pkcs7SignedData
	rest, err = asn1.Unmarshal(contentInfo.Content.Bytes, &signedData)
	if err != nil || len(rest) != 0 {
		return nil, wrap(ErrMalformedSignature, err)
	}

	// Detached signatures must not carry embedded content; accepting it would
	// let an attacker steer what is actually being verified.
	if !signedData.EncapsulatedContentInfo.ContentType.Equal(oidPKCS7Data) {
		return nil, fmt.Errorf("%w: unexpected encapsulated content type", ErrMalformedSignature)
	}
	if len(signedData.SignerInfos) != 1 {
		return nil, fmt.Errorf("%w: expected exactly one signerInfo", ErrMalformedSignature)
	}
	info := signedData.SignerInfos[0]

	if !info.DigestAlgorithm.Algorithm.Equal(oidDigestSHA256) {
		return nil, wrap(ErrUnsupportedAlgorithm, errors.New("digest algorithm must be SHA-256"))
	}
	if !info.DigestEncryptionAlgorithm.Algorithm.Equal(oidSigRSASHA256) &&
		!info.DigestEncryptionAlgorithm.Algorithm.Equal(oidSigRSAEncryption) {
		return nil, wrap(ErrUnsupportedAlgorithm, errors.New("signature algorithm must be RSA"))
	}

	certs, err := parseCertificateSet(signedData.Certificates)
	if err != nil {
		return nil, err
	}
	signer, err := findSignerCertificate(certs, info)
	if err != nil {
		return nil, err
	}
	rsaKey, ok := signer.PublicKey.(*rsa.PublicKey)
	if !ok {
		return nil, wrap(ErrUnsupportedAlgorithm, errors.New("signer key is not RSA"))
	}
	if signer.SignatureAlgorithm != x509.SHA256WithRSA &&
		signer.SignatureAlgorithm != x509.SHA256WithRSAPSS {
		return nil, wrap(ErrUnsupportedAlgorithm, errors.New("signer certificate does not use RSA-SHA256"))
	}

	signed, err := signedPayload(content, info)
	if err != nil {
		return nil, err
	}
	if err := rsa.VerifyPKCS1v15(rsaKey, crypto.SHA256, hashSHA256(signed), info.EncryptedDigest); err != nil {
		return nil, wrap(ErrInvalidSignature, err)
	}

	return &verifiedSignature{Signer: signer, CertChain: certs}, nil
}

func hashSHA256(data []byte) []byte {
	sum := crypto.SHA256.New()
	sum.Write(data)
	return sum.Sum(nil)
}

// signedPayload returns the bytes that the RSA signature is computed over and
// enforces the RFC 5652 messageDigest / contentType authenticated attributes.
func signedPayload(content []byte, info pkcs7SignerInfo) ([]byte, error) {
	if len(info.AuthenticatedAttributes) == 0 {
		return content, nil
	}

	var sawContentType, sawMessageDigest bool
	for _, attr := range info.AuthenticatedAttributes {
		switch {
		case attr.Type.Equal(oidAttrContentType):
			var contentType asn1.ObjectIdentifier
			if _, err := asn1.Unmarshal(attr.Value.Bytes, &contentType); err != nil {
				return nil, wrap(ErrMalformedSignature, err)
			}
			if !contentType.Equal(oidPKCS7Data) {
				return nil, fmt.Errorf("%w: authenticated contentType is not data", ErrMalformedSignature)
			}
			sawContentType = true
		case attr.Type.Equal(oidAttrMessageDigest):
			var digest []byte
			if _, err := asn1.Unmarshal(attr.Value.Bytes, &digest); err != nil {
				return nil, wrap(ErrMalformedSignature, err)
			}
			if !constantTimeEquals(digest, hashSHA256(content)) {
				return nil, wrap(ErrInvalidSignature, errors.New("messageDigest does not match member content"))
			}
			sawMessageDigest = true
		}
	}
	if !sawContentType || !sawMessageDigest {
		return nil, fmt.Errorf("%w: missing required authenticated attributes", ErrMalformedSignature)
	}

	// The signature covers the DER encoding of the attributes as a universal
	// SET, while the on-wire encoding uses the context-specific [0] tag.
	der, err := asn1.Marshal(info.AuthenticatedAttributes)
	if err != nil {
		return nil, wrap(ErrMalformedSignature, err)
	}
	return der, nil
}

func constantTimeEquals(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := range a {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

func parseCertificateSet(raw asn1.RawValue) ([]*x509.Certificate, error) {
	// The field is encoded as [0] IMPLICIT SET OF Certificate; after the
	// outer unmarshal, RawValue.Bytes holds the concatenated certificate DER.
	inner := raw.Bytes
	if len(inner) == 0 {
		return nil, fmt.Errorf("%w: signature carries no certificates", ErrMalformedSignature)
	}
	var certs []*x509.Certificate
	remaining := inner
	for len(remaining) > 0 {
		var certDER asn1.RawValue
		var err error
		remaining, err = asn1.Unmarshal(remaining, &certDER)
		if err != nil {
			return nil, wrap(ErrMalformedSignature, err)
		}
		cert, err := x509.ParseCertificate(certDER.FullBytes)
		if err != nil {
			return nil, wrap(ErrMalformedSignature, err)
		}
		certs = append(certs, cert)
	}
	return certs, nil
}

func findSignerCertificate(certs []*x509.Certificate, info pkcs7SignerInfo) (*x509.Certificate, error) {
	var serial asn1.RawValue
	if _, err := asn1.Unmarshal(info.IssuerAndSerialNumber.SerialNumber.FullBytes, &serial); err != nil {
		return nil, wrap(ErrMalformedSignature, err)
	}
	sidSerial := new(big.Int).SetBytes(serial.Bytes)
	for _, cert := range certs {
		sid := info.IssuerAndSerialNumber
		if string(cert.RawIssuer) == string(sid.Issuer.FullBytes) &&
			cert.SerialNumber.Cmp(sidSerial) == 0 {
			return cert, nil
		}
	}
	return nil, fmt.Errorf("%w: signer certificate not present in signature", ErrMalformedSignature)
}

func wrap(base, err error) error {
	if err == nil {
		return base
	}
	return fmt.Errorf("%w: %v", base, err)
}
