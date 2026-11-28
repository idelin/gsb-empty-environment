package pkcs12verify

import "encoding/asn1"

// ASN.1 structures from RFC 2315, RFC 2898 and RFC 7292.

type algorithmIdentifier struct {
	Algorithm  asn1.ObjectIdentifier
	Parameters asn1.RawValue `asn1:"optional"`
}

type digestInfo struct {
	Algorithm algorithmIdentifier
	Digest    []byte
}

// macData maps RFC 7292 MacData. Iteration count is optional (default 1);
// the policy layer rejects that weak default explicitly.
type macData struct {
	Mac        digestInfo
	MacSalt    []byte
	Iterations int `asn1:"optional"`
}

// contentInfo is the PKCS#7 ContentInfo. Content is absent for detached data
// and present as an explicit [0] wrapper otherwise.
type contentInfo struct {
	ContentType asn1.ObjectIdentifier
	Content     asn1.RawValue `asn1:"explicit,tag:0"`
}

type pfxPdu struct {
	Version  int
	AuthSafe contentInfo
	MacData  macData `asn1:"optional"`
}

type safeBag struct {
	ID         asn1.ObjectIdentifier
	Value      asn1.RawValue   `asn1:"tag:0,explicit"`
	Attributes []asn1.RawValue `asn1:"optional,set"`
}

type certBag struct {
	ID   asn1.ObjectIdentifier
	Data []byte `asn1:"tag:0,explicit"`
}

type encryptedPrivateKeyInfo struct {
	EncryptionAlgorithm algorithmIdentifier
	EncryptedData       []byte
}

// pbkdf2Params is PKCS#5 v2.1 PBKDF2-params. Salt is CHOICE of OCTET STRING or
// AlgorithmIdentifier; only the OCTET STRING form is accepted.
type pbkdf2Params struct {
	Salt           asn1.RawValue
	IterationCount int
	KeyLength      int                 `asn1:"optional"`
	PRF            algorithmIdentifier `asn1:"optional"`
}

type pbes2Params struct {
	KeyDerivationFunc algorithmIdentifier
	EncryptionScheme  algorithmIdentifier
}

type gcmParameters struct {
	Nonce  []byte `asn1:"tag:0,optional"`
	ICVLen int    `asn1:"tag:1,optional,default:12"`
}

// pbmac1Params mirrors PBKDF2-PBMAC1-params: keyDerivationFunc then
// messageAuthScheme.
type pbmac1Params struct {
	KeyDerivationFunc algorithmIdentifier
	MessageAuthScheme algorithmIdentifier
}

type encryptedContentInfo struct {
	ContentType                asn1.ObjectIdentifier
	ContentEncryptionAlgorithm algorithmIdentifier
	EncryptedContent           []byte `asn1:"tag:0,optional,implicit"`
}

type encryptedDataBody struct {
	Version              int
	EncryptedContentInfo encryptedContentInfo
}
