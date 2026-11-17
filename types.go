package netsec

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"math/big"
)

var (
	// OIDSignedData is the CMS content type for SignedData.
	OIDSignedData = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}

	// OIDData is the CMS content type for id-data.
	OIDData = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 1}

	// OIDContentType is the signed attribute carrying the encapsulated content
	// type (RFC 5652, section 5.3.1).
	OIDContentType = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 3}

	// OIDMessageDigest is the signed attribute carrying the digest of the
	// encapsulated content (RFC 5652, section 5.3.2).
	OIDMessageDigest = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 4}
)

// Attribute is one CMS Attribute (RFC 5652, section 5.3).
type Attribute struct {
	Type   asn1.ObjectIdentifier
	Values []asn1.RawValue
}

// SignerInfo is one SignerInfo value from a CMS SignedData structure.
type SignerInfo struct {
	Version int

	// Issuer and SerialNumber identify the signer when the issuer-and-serial
	// form of SignerIdentifier is used.
	Issuer       []byte
	SerialNumber *big.Int

	// SubjectKeyIdentifier identifies the signer when the subjectKeyIdentifier
	// form of SignerIdentifier is used.
	SubjectKeyIdentifier []byte

	DigestAlgorithm    pkix.AlgorithmIdentifier
	SignatureAlgorithm pkix.AlgorithmIdentifier

	// HasSignedAttrs reports whether the [0] IMPLICIT signedAttrs field was
	// present. An empty signedAttrs field is still present, and is invalid
	// because the required attributes are missing.
	HasSignedAttrs bool

	// SignedAttrs contains the parsed signed attributes. Values preserves the
	// original DER representation of each attribute value.
	SignedAttrs []Attribute

	// signedAttrsContent is the content octet string of the implicit [0] tag.
	// The signature is calculated over the same octets re-tagged as a DER
	// SET OF.
	signedAttrsContent []byte

	Signature     []byte
	UnsignedAttrs []Attribute

	// Certificate is the certificate selected by this SignerInfo's
	// SignerIdentifier, if it was present in the SignedData certificate set.
	Certificate *x509.Certificate
}

// SignedAttrsDER returns the DER encoding used as the signature input for a
// SignerInfo that has signed attributes. RFC 5652 section 5.4 requires the
// implicit context-specific [0] tag to be replaced by the universal SET OF
// tag for this calculation.
//
// The attribute content octets are preserved and only the outer tag and
// length are encoded canonically. Re-encoding the individual attributes would
// change the bytes that the signer actually signed.
func (si *SignerInfo) SignedAttrsDER() []byte {
	if !si.HasSignedAttrs {
		return nil
	}
	return encodeDER(derClassUniversal, derTagSet, true, si.signedAttrsContent)
}

// FindAttributes returns all attributes with the given type. More than one
// result indicates a duplicate attribute, which verification rejects.
func (si *SignerInfo) FindAttributes(oid asn1.ObjectIdentifier) []Attribute {
	var found []Attribute
	for _, attr := range si.SignedAttrs {
		if attr.Type.Equal(oid) {
			found = append(found, attr)
		}
	}
	return found
}

// SignedData is a parsed CMS SignedData structure.
type SignedData struct {
	Version          int
	DigestAlgorithms []pkix.AlgorithmIdentifier

	// ContentType is the encapsulated content type (eContentType).
	ContentType asn1.ObjectIdentifier

	// Content is the encapsulated content. HasContent distinguishes an absent
	// eContent (detached signature) from an empty OCTET STRING.
	Content    []byte
	HasContent bool

	Certificates []*x509.Certificate
	SignerInfos  []*SignerInfo
}
