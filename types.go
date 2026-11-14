package netsec

import (
	"crypto"
	"crypto/x509"
	"encoding/asn1"
	"fmt"
	"math/big"
	"strings"
	"time"
)

var (
	OIDContentTypeSignedData   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}
	OIDContentTypeTSTInfo      = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 16, 1, 4}
	OIDTSTInfo                 = OIDContentTypeTSTInfo
	OIDSignedData              = OIDContentTypeSignedData
	OIDSigningTime             = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 5}
	OIDMessageDigest           = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 4}
	OIDContentType             = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 3}
	OIDSignatureTimestampToken = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 16, 2, 14}
	OIDExtKeyUsageTimeStamping = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 3, 8}
	OIDSubjectKeyIdentifier    = asn1.ObjectIdentifier{2, 5, 29, 14}
	OIDAuthorityKeyIdentifier  = asn1.ObjectIdentifier{2, 5, 29, 35}
	OIDHashMD5                 = asn1.ObjectIdentifier{1, 2, 840, 113549, 2, 5}
	OIDHashSHA1                = asn1.ObjectIdentifier{1, 3, 14, 3, 2, 26}
	OIDHashSHA224              = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 4}
	OIDHashSHA256              = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1}
	OIDHashSHA384              = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 2}
	OIDHashSHA512              = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 3}
	OIDHashSHA512_224          = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 5}
	OIDHashSHA512_256          = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 6}
	OIDHashSHA3_224            = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 7}
	OIDHashSHA3_256            = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 8}
	OIDHashSHA3_384            = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 9}
	OIDHashSHA3_512            = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 10}
	OIDSHA256                  = OIDHashSHA256
)

// AlgorithmIdentifier is the ASN.1 AlgorithmIdentifier used by CMS.
type AlgorithmIdentifier struct {
	OID        asn1.ObjectIdentifier
	Parameters asn1.RawValue
	Name       string
}

// MessageImprint is the RFC 3161 MessageImprint value.
type MessageImprint struct {
	HashAlgorithm asn1.ObjectIdentifier
	Hash          crypto.Hash
	HashName      string
	HashedMessage []byte
}

// Digest is an alias for the message imprint digest bytes.
func (m MessageImprint) Digest() []byte {
	return m.HashedMessage
}

// Algorithm returns the imprint hash OID.
func (m MessageImprint) Algorithm() asn1.ObjectIdentifier {
	return m.HashAlgorithm
}

// Attribute is a CMS signed attribute.
type Attribute struct {
	Type   asn1.ObjectIdentifier
	Values []asn1.RawValue
}

// TokenSigner describes one SignerInfo in the CMS SignedData structure.
type TokenSigner struct {
	Certificate          *x509.Certificate
	IssuerIdentity       IssuerIdentity
	IssuerName           string
	SerialNumber         *big.Int
	DigestAlgorithm      AlgorithmIdentifier
	SignatureAlgorithm   AlgorithmIdentifier
	SignedAttributes     []Attribute
	Signature            []byte
	SignatureVerified    bool
	SubjectKeyIdentifier []byte
	SigningTime          time.Time
}

// IssuerIdentity is the stable identity of the TSA certificate used in cache
// keys. It is derived from the certificate subject and public key.
type IssuerIdentity struct {
	Subject           string
	RawSubject        []byte
	CertificateSHA256 [32]byte
	PublicKeySHA256   [32]byte
}

func (id IssuerIdentity) String() string {
	return "sha256:" + hexString(id.CertificateSHA256[:])
}

// Equal reports whether two identities identify the same TSA certificate.
func (id IssuerIdentity) Equal(other IssuerIdentity) bool {
	return id.CertificateSHA256 == other.CertificateSHA256
}

// TimestampToken is a parsed RFC 3161 TimeStampToken.
type TimestampToken struct {
	Raw                []byte
	ContentType        asn1.ObjectIdentifier
	EncapContentType   asn1.ObjectIdentifier
	Version            int
	Policy             asn1.ObjectIdentifier
	MessageImprint     MessageImprint
	SerialNumber       *big.Int
	GenTime            time.Time
	Accuracy           *Accuracy
	Ordering           bool
	Nonce              *big.Int
	TSAName            *asn1.RawValue
	SignerCertificates []*x509.Certificate
	Certificates       []*x509.Certificate
	Signers            []TokenSigner
	IssuerIdentity     IssuerIdentity
	IssuerName         string
	Issuer             string
	TimestampSigner    *x509.Certificate
	HashAlgorithm      asn1.ObjectIdentifier
	HashedMessage      []byte
	Imprint            MessageImprint
	SigningTime        time.Time
	SignatureTimestamp time.Time
}

// Accuracy is the optional RFC 3161 accuracy value.
type Accuracy struct {
	Seconds int
	Millis  int
	Micros  int
}

// Binding is the result of successfully binding a timestamp to a signature.
type Binding struct {
	Timestamp                  time.Time
	GenTime                    time.Time
	Policy                     asn1.ObjectIdentifier
	SerialNumber               *big.Int
	Imprint                    MessageImprint
	ArtifactDigest             []byte
	Issuer                     IssuerIdentity
	IssuerName                 string
	TimestampSignerCertificate *x509.Certificate
	SignerCertificate          *x509.Certificate
	Chain                      []*x509.Certificate
	Token                      *TimestampToken
	CacheHit                   bool
}

// Covers reports whether this binding's imprint covers signature using the
// token's declared hash algorithm.
func (b *Binding) Covers(signature []byte) bool {
	if b == nil {
		return false
	}
	return imprintMatches(b.Imprint, signature)
}

// VerificationRequest describes one signature and its timestamp token.
//
// Token, or Tokens with exactly one entry, identifies the timestamp. Signature
// is the code-signing signature that the timestamp must cover. Digest and
// HashAlgorithm may be supplied instead of Signature when the caller already
// has the signature digest.
type VerificationRequest struct {
	Token             []byte
	Tokens            [][]byte
	Signature         []byte
	Digest            []byte
	HashAlgorithm     crypto.Hash
	SignerCertificate *x509.Certificate
}

// RevocationChecker reports whether a certificate is revoked for the supplied
// verification time.
type RevocationChecker interface {
	IsRevoked(cert *x509.Certificate, at time.Time) (bool, error)
}

type RevocationCheckerFunc func(cert *x509.Certificate, at time.Time) (bool, error)

func (f RevocationCheckerFunc) IsRevoked(cert *x509.Certificate, at time.Time) (bool, error) {
	return f(cert, at)
}

func hexString(value []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, len(value)*2)
	for i, v := range value {
		out[i*2] = digits[v>>4]
		out[i*2+1] = digits[v&0x0f]
	}
	return string(out)
}

func normalizeIdentity(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), " "))
}

func (id IssuerIdentity) description() string {
	if id.Subject != "" {
		return id.Subject
	}
	return id.String()
}

func (s TokenSigner) signerName() string {
	if s.Certificate == nil {
		return ""
	}
	if s.Certificate.Subject.CommonName != "" {
		return s.Certificate.Subject.CommonName
	}
	return s.Certificate.Subject.String()
}

func (t *TimestampToken) primarySigner() *TokenSigner {
	if t == nil {
		return nil
	}
	for i := range t.Signers {
		if t.Signers[i].SignatureVerified && t.Signers[i].Certificate != nil {
			return &t.Signers[i]
		}
	}
	for i := range t.Signers {
		if t.Signers[i].Certificate != nil {
			return &t.Signers[i]
		}
	}
	return nil
}

func (t *TimestampToken) String() string {
	if t == nil {
		return "<nil>"
	}
	return fmt.Sprintf("TimestampToken{policy=%s serial=%v issuer=%s time=%s}",
		t.Policy.String(), t.SerialNumber, t.IssuerName, t.GenTime.UTC().Format(time.RFC3339Nano))
}

func hashName(h crypto.Hash) string {
	switch h {
	case crypto.MD5:
		return "MD5"
	case crypto.SHA1:
		return "SHA-1"
	case crypto.SHA224:
		return "SHA-224"
	case crypto.SHA256:
		return "SHA-256"
	case crypto.SHA384:
		return "SHA-384"
	case crypto.SHA512:
		return "SHA-512"
	case crypto.SHA512_224:
		return "SHA-512/224"
	case crypto.SHA512_256:
		return "SHA-512/256"
	case crypto.SHA3_224:
		return "SHA3-224"
	case crypto.SHA3_256:
		return "SHA3-256"
	case crypto.SHA3_384:
		return "SHA3-384"
	case crypto.SHA3_512:
		return "SHA3-512"
	default:
		return ""
	}
}
