package netsec

import (
	"bytes"
	"crypto"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/asn1"
	"fmt"
	"hash"
)

// VerifySignedData verifies every SignerInfo in a DER-encoded CMS
// SignedData ContentInfo and returns the certificates that verified.
//
// content is used only for detached signatures, i.e. when the SignedData
// has no eContent; for attached signatures it must be nil. It performs
// cryptographic signature validation and the RFC 5652 signed-attribute
// checks but does not build a certificate chain. Use VerifySignedDataWithRoots
// when chain validation against a trust store is required.
func VerifySignedData(data, content []byte) ([]*x509.Certificate, error) {
	return verifySignedData(data, content, nil)
}

// VerifySignedDataWithRoots behaves like VerifySignedData and additionally
// builds a chain from each signer certificate to a root in roots. A nil
// pool means chain validation is skipped.
func VerifySignedDataWithRoots(data, content []byte, roots *x509.CertPool) ([]*x509.Certificate, error) {
	return verifySignedData(data, content, roots)
}

func verifySignedData(data, detachedContent []byte, roots *x509.CertPool) ([]*x509.Certificate, error) {
	var ci ContentInfo
	if rest, err := asn1.Unmarshal(data, &ci); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSignedData, err)
	} else if len(rest) > 0 {
		return nil, fmt.Errorf("%w: trailing data after ContentInfo", ErrInvalidSignedData)
	}
	if !ci.ContentType.Equal(oidSignedData) {
		return nil, fmt.Errorf("%w: content type %s is not signedData", ErrInvalidSignedData, ci.ContentType.String())
	}

	var sd SignedData
	if rest, err := asn1.Unmarshal(ci.Content.Bytes, &sd); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSignedData, err)
	} else if len(rest) > 0 {
		return nil, fmt.Errorf("%w: trailing data after SignedData", ErrInvalidSignedData)
	}
	if len(sd.SignerInfos) == 0 {
		return nil, ErrNoSigners
	}

	certificates, err := parseCertificates(sd.Certificates)
	if err != nil {
		return nil, err
	}

	verified := make([]*x509.Certificate, 0, len(sd.SignerInfos))
	for i := range sd.SignerInfos {
		signer := &sd.SignerInfos[i]
		cert, err := findCertificate(signer.SID, certificates)
		if err != nil {
			return nil, err
		}

		content, err := resolveContent(sd.EncapContentInfo, detachedContent)
		if err != nil {
			return nil, err
		}

		hashFunc, err := digestForOID(signer.DigestAlgorithm.Algorithm)
		if err != nil {
			return nil, err
		}

		signedInput, err := signedDataInput(signer, sd.EncapContentInfo.EContentType, hashFunc, content)
		if err != nil {
			return nil, err
		}

		if err := verifySignature(cert, signer, hashFunc, signedInput); err != nil {
			return nil, err
		}

		if roots != nil {
			if err := verifyChain(cert, roots); err != nil {
				return nil, err
			}
		}
		verified = append(verified, cert)
	}
	return verified, nil
}

// signedDataInput returns the bytes that the SignerInfo signature is
// computed over. With signedAttrs the input is the DER encoding of the
// attributes as a universal SET (RFC 5652 section 5.4); without them the
// input is the raw encapsulated content octets (section 5.5).
func signedDataInput(signer *SignerInfo, eContentType asn1.ObjectIdentifier, hashFunc crypto.Hash, content []byte) ([]byte, error) {
	if len(signer.SignedAttrs.Bytes) == 0 && len(signer.SignedAttrs.FullBytes) == 0 {
		return content, nil
	}

	attrs, encoded, err := canonicalSignedAttrs(signer.SignedAttrs)
	if err != nil {
		return nil, err
	}

	contentDigest := hashBytes(hashFunc, content)
	if err := checkRequiredAttributes(attrs, eContentType, contentDigest); err != nil {
		return nil, err
	}
	return encoded, nil
}

// canonicalSignedAttrs re-encodes signedAttrs for signature verification:
// the IMPLICIT [0] tag (0xA0) of the on-the-wire encoding is replaced by
// the universal SET tag (0x31) without touching length, contents or order,
// per RFC 5652 section 5.4. The same bytes are also parsed back into
// Attribute structures so the required-attribute checks run on the exact
// signed bytes.
func canonicalSignedAttrs(raw asn1.RawValue) ([]Attribute, []byte, error) {
	if raw.Class != asn1.ClassContextSpecific || raw.Tag != 0 || !raw.IsCompound {
		return nil, nil, fmt.Errorf("%w: signedAttrs must be IMPLICIT [0] SET", ErrInvalidSignedData)
	}
	if len(raw.FullBytes) == 0 || raw.FullBytes[0] != 0xA0 {
		return nil, nil, fmt.Errorf("%w: signedAttrs encoding missing A0 tag", ErrInvalidSignedData)
	}

	encoded := make([]byte, len(raw.FullBytes))
	copy(encoded, raw.FullBytes)
	encoded[0] = 0x31 // universal constructed SET.

	var attrs []Attribute
	// encoding/asn1 only accepts SEQUENCE OF when unmarshalling into a
	// slice, so walk the SET contents element by element instead.
	remain := raw.Bytes
	for len(remain) > 0 {
		var attr Attribute
		var err error
		remain, err = asn1.Unmarshal(remain, &attr)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: cannot parse signedAttrs: %v", ErrInvalidSignedData, err)
		}
		attrs = append(attrs, attr)
	}

	return attrs, encoded, nil
}

// checkRequiredAttributes enforces the RFC 5652 signed-attribute rules:
// content-type and message-digest must each appear exactly once, no OID may
// be duplicated, content-type must match eContentType and message-digest
// must match the digest of the encapsulated content.
func checkRequiredAttributes(attrs []Attribute, eContentType asn1.ObjectIdentifier, contentDigest []byte) error {
	seen := make(map[string]int, len(attrs))
	for _, attr := range attrs {
		key := attr.Type.String()
		seen[key]++
		if seen[key] > 1 {
			return fmt.Errorf("%w: %s", ErrDuplicateSignedAttribute, key)
		}
		if len(attr.Values) == 0 {
			return fmt.Errorf("%w: %s has no value", ErrMissingSignedAttribute, key)
		}
	}

	contentTypeAttr, ok := findAttribute(attrs, oidContentType)
	if !ok {
		return fmt.Errorf("%w: content-type", ErrMissingSignedAttribute)
	}
	if len(contentTypeAttr.Values) != 1 {
		return fmt.Errorf("%w: content-type must be single-valued", ErrMissingSignedAttribute)
	}
	var attrContentType asn1.ObjectIdentifier
	if _, err := asn1.Unmarshal(contentTypeAttr.Values[0].FullBytes, &attrContentType); err != nil {
		return fmt.Errorf("%w: content-type is not an OID: %v", ErrMissingSignedAttribute, err)
	}
	if !attrContentType.Equal(eContentType) {
		return fmt.Errorf("%w: %s != %s", ErrContentTypeMismatch, attrContentType.String(), eContentType.String())
	}

	digestAttr, ok := findAttribute(attrs, oidMessageDigest)
	if !ok {
		return fmt.Errorf("%w: message-digest", ErrMissingSignedAttribute)
	}
	if len(digestAttr.Values) != 1 {
		return fmt.Errorf("%w: message-digest must be single-valued", ErrMissingSignedAttribute)
	}
	var attrDigest []byte
	if _, err := asn1.Unmarshal(digestAttr.Values[0].FullBytes, &attrDigest); err != nil {
		return fmt.Errorf("%w: message-digest is not an OCTET STRING: %v", ErrMissingSignedAttribute, err)
	}
	if !bytes.Equal(attrDigest, contentDigest) {
		return ErrDigestMismatch
	}
	return nil
}

func findAttribute(attrs []Attribute, oid asn1.ObjectIdentifier) (Attribute, bool) {
	for _, attr := range attrs {
		if attr.Type.Equal(oid) {
			return attr, true
		}
	}
	return Attribute{}, false
}

// resolveContent returns the raw content octets that digests are computed
// over. Attached content must live in eContent (OCTET STRING); detached
// signatures require the caller-supplied content. Passing external content
// alongside attached content is rejected so callers cannot shadow signed
// content.
func resolveContent(eci EncapsulatedContentInfo, detached []byte) ([]byte, error) {
	if len(eci.EContent.FullBytes) > 0 {
		if len(detached) > 0 {
			return nil, fmt.Errorf("%w: external content supplied for attached signature", ErrContentMismatch)
		}
		var octets []byte
		if rest, err := asn1.Unmarshal(eci.EContent.Bytes, &octets); err != nil {
			return nil, fmt.Errorf("%w: eContent is not an OCTET STRING: %v", ErrInvalidSignedData, err)
		} else if len(rest) > 0 {
			return nil, fmt.Errorf("%w: trailing data in eContent", ErrInvalidSignedData)
		}
		return octets, nil
	}
	if len(detached) == 0 {
		return nil, fmt.Errorf("%w: signature has no eContent and no detached content was provided", ErrContentMismatch)
	}
	return detached, nil
}

func parseCertificates(raw asn1.RawValue) ([]*x509.Certificate, error) {
	if len(raw.Bytes) == 0 {
		return nil, nil
	}
	var certs []*x509.Certificate
	remain := raw.Bytes
	for len(remain) > 0 {
		var entry asn1.RawValue
		var err error
		remain, err = asn1.Unmarshal(remain, &entry)
		if err != nil {
			return nil, fmt.Errorf("%w: cannot parse certificate set: %v", ErrInvalidSignedData, err)
		}
		// CertificateChoices: plain Certificate is the only universal
		// [SEQUENCE] alternative; others are context-specific.
		if entry.Class == asn1.ClassUniversal && entry.Tag == asn1.TagSequence {
			cert, err := x509.ParseCertificate(entry.FullBytes)
			if err != nil {
				return nil, fmt.Errorf("%w: %v", ErrInvalidSignedData, err)
			}
			certs = append(certs, cert)
		}
	}
	return certs, nil
}

func findCertificate(sid SignerIdentifier, certs []*x509.Certificate) (*x509.Certificate, error) {
	for _, cert := range certs {
		switch {
		case len(sid.IssuerAndSerialNumber.Issuer.FullBytes) > 0:
			if bytes.Equal(cert.RawIssuer, sid.IssuerAndSerialNumber.Issuer.FullBytes) &&
				sid.IssuerAndSerialNumber.SerialNumber != nil &&
				cert.SerialNumber.Cmp(sid.IssuerAndSerialNumber.SerialNumber) == 0 {
				return cert, nil
			}
		case len(sid.SubjectKeyIdentifier) > 0:
			if bytes.Equal(cert.SubjectKeyId, sid.SubjectKeyIdentifier) {
				return cert, nil
			}
		}
	}
	return nil, fmt.Errorf("%w", ErrCertificateNotFound)
}

func digestForOID(oid asn1.ObjectIdentifier) (crypto.Hash, error) {
	switch {
	case oid.Equal(oidSHA224):
		return crypto.SHA224, nil
	case oid.Equal(oidSHA256):
		return crypto.SHA256, nil
	case oid.Equal(oidSHA384):
		return crypto.SHA384, nil
	case oid.Equal(oidSHA512):
		return crypto.SHA512, nil
	default:
		return 0, fmt.Errorf("%w: digest %s", ErrUnsupportedAlgorithm, oid.String())
	}
}

func hashBytes(hashFunc crypto.Hash, content []byte) []byte {
	var h hash.Hash
	switch hashFunc {
	case crypto.SHA224:
		h = sha256.New224()
	case crypto.SHA256:
		h = sha256.New()
	case crypto.SHA384:
		h = sha512.New384()
	case crypto.SHA512:
		h = sha512.New()
	default:
		panic("netsec: unsupported hash")
	}
	h.Write(content)
	return h.Sum(nil)
}
