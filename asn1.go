package ocspcheck

import (
	"crypto"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
)

// Well-known OIDs used by OCSP (RFC 6960) and certificate key usages.
var (
	// id-pkix-ocsp-basic: the response type carried in ResponseBytes.
	oidBasicResponse = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 48, 1, 1}

	// id-kp-OCSPSigning.
	oidExtKeyOCSPSigning = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 3, 9}

	oidHashSHA1   = asn1.ObjectIdentifier{1, 3, 14, 3, 2, 26}
	oidHashSHA256 = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1}
	oidHashSHA384 = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 2}
	oidHashSHA512 = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 3}

	oidSigSHA1WithRSA     = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 5}
	oidSigSHA256WithRSA   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 11}
	oidSigSHA384WithRSA   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 12}
	oidSigSHA512WithRSA   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 13}
	oidSigRSAPSS          = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 10}
	oidSigECDSAWithSHA1   = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 1}
	oidSigECDSAWithSHA256 = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 2}
	oidSigECDSAWithSHA384 = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 3}
	oidSigECDSAWithSHA512 = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 4}
	oidSigEd25519         = asn1.ObjectIdentifier{1, 3, 101, 112}
)

// oidToHash maps CertID hashAlgorithm OIDs onto crypto hashes. SHA-1 is kept
// because RFC 6960 mandates it for the default CertID; name/key hashes here
// are non-security-sensitive identifiers rather than trust anchors.
var oidToHash = map[string]crypto.Hash{
	oidHashSHA1.String():   crypto.SHA1,
	oidHashSHA256.String(): crypto.SHA256,
	oidHashSHA384.String(): crypto.SHA384,
	oidHashSHA512.String(): crypto.SHA512,
}

type sigAlgorithm struct {
	hash crypto.Hash
	pss  bool
}

// ed25519Hash is a sentinel: Ed25519 signs the raw TBS bytes rather than a
// pre-image digest.
const ed25519Hash = crypto.Hash(0)

var sigAlgorithms = map[string]sigAlgorithm{
	oidSigSHA1WithRSA.String():     {hash: crypto.SHA1},
	oidSigSHA256WithRSA.String():   {hash: crypto.SHA256},
	oidSigSHA384WithRSA.String():   {hash: crypto.SHA384},
	oidSigSHA512WithRSA.String():   {hash: crypto.SHA512},
	oidSigRSAPSS.String():          {hash: crypto.SHA256, pss: true},
	oidSigECDSAWithSHA1.String():   {hash: crypto.SHA1},
	oidSigECDSAWithSHA256.String(): {hash: crypto.SHA256},
	oidSigECDSAWithSHA384.String(): {hash: crypto.SHA384},
	oidSigECDSAWithSHA512.String(): {hash: crypto.SHA512},
	oidSigEd25519.String():         {hash: ed25519Hash},
}

// ocspResponse is the outer envelope returned over HTTP or stapled in TLS.
type ocspResponse struct {
	Status        asn1.Enumerated
	ResponseBytes []responseBytes `asn1:"explicit,tag:0,optional"`
}

type responseBytes struct {
	ResponseType asn1.ObjectIdentifier
	Response     asn1.RawValue
}

type basicOCSPResponse struct {
	tbs          responseData
	signature    asn1.BitString
	certificates []*x509.Certificate
}

type responseData struct {
	Version            int `asn1:"optional,default:0,explicit,tag:0"`
	ResponderID        asn1.RawValue
	ProducedAt         timeValue
	Responses          []singleResponse
	ResponseExtensions []pkix.Extension `asn1:"explicit,tag:1,optional"`

	// RawTBSCertificate holds the complete DER ResponseData SEQUENCE, which
	// is exactly the bytes covered by the signature.
	RawTBSCertificate asn1.RawValue

	// SignatureAlgorithm belongs to BasicOCSPResponse but is stored here for
	// convenient access during verification.
	SignatureAlgorithm pkix.AlgorithmIdentifier
}

type certID struct {
	HashAlgorithm pkix.AlgorithmIdentifier
	NameHash      []byte
	KeyHash       []byte
	SerialNumber  *bigInt
}

type singleResponse struct {
	CertID certID
	// Status is the certificateStatus CHOICE: implicit [0] good, [1] revoked
	// (containing GeneralizedTime), or [2] unknown. Its Tag is interpreted in
	// parse.go.
	Status     asn1.RawValue
	ThisUpdate timeValue
	NextUpdate timeValue        `asn1:"explicit,tag:0,optional"`
	Extensions []pkix.Extension `asn1:"explicit,tag:1,optional"`
}
