package netsec

import (
	"encoding/asn1"
	"math/big"
)

// ContentInfo is the top-level CMS envelope (RFC 5652 section 3).
type ContentInfo struct {
	ContentType asn1.ObjectIdentifier
	Content     asn1.RawValue `asn1:"explicit,tag:0"`
}

// SignedData is the payload carried inside a SignedData ContentInfo
// (RFC 5652 section 5.1).
type SignedData struct {
	Version          int
	DigestAlgorithms []AlgorithmIdentifier `asn1:"set"`
	EncapContentInfo EncapsulatedContentInfo
	Certificates     asn1.RawValue   `asn1:"optional,implicit,tag:0"`
	CRLs             []asn1.RawValue `asn1:"optional,implicit,tag:1"`
	SignerInfos      []SignerInfo    `asn1:"set,min:1"`
}

// AlgorithmIdentifier mirrors the ASN.1 type of the same name.
type AlgorithmIdentifier struct {
	Algorithm  asn1.ObjectIdentifier
	Parameters asn1.RawValue `asn1:"optional"`
}

// EncapsulatedContentInfo carries the signed content (RFC 5652 section 5.2).
// EContent is optional so that detached signatures can be represented.
type EncapsulatedContentInfo struct {
	EContentType asn1.ObjectIdentifier
	EContent     asn1.RawValue `asn1:"optional,explicit,tag:0"`
}

// SignerInfo describes a single signer (RFC 5652 section 5.3).
// SignedAttrs and UnsignedAttrs are kept as RawValues because
// SignedAttrs uses the IMPLICIT [0] tag while its signed representation
// must be a universal SET (tag 0x31).
type SignerInfo struct {
	Version            int
	SID                SignerIdentifier
	DigestAlgorithm    AlgorithmIdentifier
	SignedAttrs        asn1.RawValue `asn1:"optional,implicit,tag:0"`
	SignatureAlgorithm AlgorithmIdentifier
	Signature          []byte
	UnsignedAttrs      asn1.RawValue `asn1:"optional,implicit,tag:1"`
}

// SignerIdentifier is the CHOICE between issuerAndSerialNumber and
// subjectKeyIdentifier ([0] IMPLICIT).
type SignerIdentifier struct {
	IssuerAndSerialNumber IssuerAndSerialNumber `asn1:"optional"`
	SubjectKeyIdentifier  []byte                `asn1:"optional,implicit,tag:0"`
}

// IssuerAndSerialNumber pairs an X.500 Name with a certificate serial number.
type IssuerAndSerialNumber struct {
	Issuer       asn1.RawValue
	SerialNumber *big.Int
}

// Attribute is a CMS signed or unsigned attribute (RFC 5652 section 3).
type Attribute struct {
	Type   asn1.ObjectIdentifier
	Values []asn1.RawValue `asn1:"set"`
}

// rsaPSSParams captures the parameters of id-RSASSA-PSS (RFC 4055).
type rsaPSSParams struct {
	HashAlg      AlgorithmIdentifier `asn1:"optional,implicit,tag:0"`
	MaskAlg      pssMaskGenAlgorithm `asn1:"optional,implicit,tag:1"`
	SaltLength   int                 `asn1:"optional,implicit,tag:2,default:20"`
	TrailerField int                 `asn1:"optional,implicit,tag:3,default:1"`
}

type pssMaskGenAlgorithm struct {
	Algorithm  asn1.ObjectIdentifier
	Parameters AlgorithmIdentifier `asn1:"optional"`
}
