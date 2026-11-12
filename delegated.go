package netsec

import (
	"bytes"
	"crypto"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"fmt"
	"time"
)

const (
	// MaxDelegatedCredentialValidity is the RFC 9345 default maximum lifetime
	// for a delegated credential.
	MaxDelegatedCredentialValidity = 7 * 24 * time.Hour

	// DelegatedCredentialServerContext is the RFC 9345 signature context for
	// a delegated credential used for server authentication.
	DelegatedCredentialServerContext = "TLS, server delegated credentials"
	// DelegatedCredentialClientContext is the RFC 9345 signature context for
	// a delegated credential used for client authentication.
	DelegatedCredentialClientContext = "TLS, client delegated credentials"

	// CertificateVerifyServerContext is the TLS 1.3 CertificateVerify context
	// for server authentication.
	CertificateVerifyServerContext = "TLS 1.3, server CertificateVerify"
	// CertificateVerifyClientContext is the TLS 1.3 CertificateVerify context
	// for client authentication.
	CertificateVerifyClientContext = "TLS 1.3, client CertificateVerify"
)

var (
	// ErrMalformedCredential indicates a malformed DelegatedCredential or
	// SubjectPublicKeyInfo.
	ErrMalformedCredential = errors.New("netsec: malformed delegated credential")
	// ErrUnsupportedAlgorithm indicates a signature scheme or key type that
	// the verifier will not accept.
	ErrUnsupportedAlgorithm = errors.New("netsec: unsupported or disallowed algorithm")
	// ErrAlgorithmMismatch indicates that the declared algorithm does not
	// match the key, CertificateVerify scheme, or credential signature.
	ErrAlgorithmMismatch = errors.New("netsec: signature algorithm mismatch")
	// ErrCredentialExpired indicates that now is past the credential expiry.
	ErrCredentialExpired = errors.New("netsec: delegated credential expired")
	// ErrValidityTooLong indicates a lifetime over seven days or beyond the
	// delegation certificate's validity.
	ErrValidityTooLong = errors.New("netsec: delegated credential validity exceeds limit")
	// ErrCertificateInvalid indicates that the delegation certificate is not
	// currently valid.
	ErrCertificateInvalid = errors.New("netsec: delegation certificate invalid")
	// ErrCertificateUsage indicates a missing DelegationUsage extension,
	// missing digitalSignature usage, or a role mismatch.
	ErrCertificateUsage = errors.New("netsec: delegation certificate usage invalid")
	// ErrSignatureInvalid indicates that a credential or CertificateVerify
	// signature did not verify.
	ErrSignatureInvalid = errors.New("netsec: signature verification failed")
)

// Role selects the RFC 9345 context string used for the delegated credential
// and the subsequent TLS 1.3 CertificateVerify signature.
type Role uint8

const (
	RoleServer Role = iota + 1
	RoleClient
)

func (r Role) valid() bool {
	return r == RoleServer || r == RoleClient
}

func (r Role) String() string {
	switch r {
	case RoleServer:
		return "server"
	case RoleClient:
		return "client"
	default:
		return fmt.Sprintf("role(%d)", uint8(r))
	}
}

func (r Role) delegatedContext() (string, error) {
	switch r {
	case RoleServer:
		return DelegatedCredentialServerContext, nil
	case RoleClient:
		return DelegatedCredentialClientContext, nil
	default:
		return "", fmt.Errorf("%w: invalid role", ErrCertificateUsage)
	}
}

func (r Role) certificateVerifyContext() (string, error) {
	switch r {
	case RoleServer:
		return CertificateVerifyServerContext, nil
	case RoleClient:
		return CertificateVerifyClientContext, nil
	default:
		return "", fmt.Errorf("%w: invalid role", ErrCertificateUsage)
	}
}

// DelegatedCredential is the decoded RFC 9345 DelegatedCredential structure.
type DelegatedCredential struct {
	// ValidTime is the credential lifetime in seconds, relative to the
	// delegation certificate's NotBefore time.
	ValidTime uint32
	// CertVerifyAlgorithm is dc_cert_verify_algorithm. It must match the
	// algorithm in the peer's CertificateVerify message and identifies the
	// delegated public key's signature algorithm.
	CertVerifyAlgorithm SignatureScheme
	// PublicKey is the parsed ASN1_subjectPublicKeyInfo.
	PublicKey crypto.PublicKey
	// PublicKeyDER is the exact DER-encoded SubjectPublicKeyInfo.
	PublicKeyDER []byte
	// Algorithm is the signature scheme used to sign the credential with the
	// delegation certificate's private key.
	Algorithm SignatureScheme
	// Signature is the delegation signature.
	Signature []byte

	rawCred           []byte
	publicKeyOID      asn1.ObjectIdentifier
	publicKeyPSSParms *asn1.RawValue
}

// ParseDelegatedCredential parses the RFC 9345 wire encoding. The input is the
// contents of the delegated_credential extension, without any TLS extension
// framing.
func ParseDelegatedCredential(raw []byte) (*DelegatedCredential, error) {
	fixed, err := parseDelegatedCredentialPrefix(raw)
	if err != nil {
		return nil, err
	}
	return fixed, nil
}

func parseDelegatedCredentialPrefix(raw []byte) (*DelegatedCredential, error) {
	rest := raw
	read := func(n int) ([]byte, error) {
		if n < 0 || len(rest) < n {
			return nil, fmt.Errorf("%w: truncated input", ErrMalformedCredential)
		}
		out := rest[:n]
		rest = rest[n:]
		return out, nil
	}

	validTimeBytes, err := read(4)
	if err != nil {
		return nil, err
	}
	algorithmBytes, err := read(2)
	if err != nil {
		return nil, err
	}
	publicKeyLengthBytes, err := read(3)
	if err != nil {
		return nil, err
	}

	publicKeyLength := int(publicKeyLengthBytes[0])<<16 | int(publicKeyLengthBytes[1])<<8 | int(publicKeyLengthBytes[2])
	if publicKeyLength < 1 {
		return nil, fmt.Errorf("%w: empty public key", ErrMalformedCredential)
	}
	publicKeyDER, err := read(publicKeyLength)
	if err != nil {
		return nil, err
	}

	credBytes := make([]byte, len(raw)-len(rest))
	copy(credBytes, raw[:len(credBytes)])

	delegationAlgorithmBytes, err := read(2)
	if err != nil {
		return nil, err
	}
	signatureLengthBytes, err := read(2)
	if err != nil {
		return nil, err
	}
	signatureLength := int(signatureLengthBytes[0])<<8 | int(signatureLengthBytes[1])
	if signatureLength < 1 {
		return nil, fmt.Errorf("%w: empty signature", ErrMalformedCredential)
	}
	signature, err := read(signatureLength)
	if err != nil {
		return nil, err
	}
	if len(rest) != 0 {
		return nil, fmt.Errorf("%w: trailing data", ErrMalformedCredential)
	}

	publicKey, keyOID, pssParams, err := parseSubjectPublicKeyInfo(publicKeyDER)
	if err != nil {
		return nil, err
	}

	return &DelegatedCredential{
		ValidTime:           uint32(validTimeBytes[0])<<24 | uint32(validTimeBytes[1])<<16 | uint32(validTimeBytes[2])<<8 | uint32(validTimeBytes[3]),
		CertVerifyAlgorithm: SignatureScheme(uint16(algorithmBytes[0])<<8 | uint16(algorithmBytes[1])),
		PublicKey:           publicKey,
		PublicKeyDER:        append([]byte(nil), publicKeyDER...),
		Algorithm:           SignatureScheme(uint16(delegationAlgorithmBytes[0])<<8 | uint16(delegationAlgorithmBytes[1])),
		Signature:           append([]byte(nil), signature...),
		rawCred:             credBytes,
		publicKeyOID:        keyOID,
		publicKeyPSSParms:   pssParams,
	}, nil
}

// DelegatedCredentialSigningData constructs the exact byte string that RFC
// 9345 says is signed by the delegation certificate's private key:
//
//	64 spaces || context || 0x00 || DER certificate || Credential || algorithm
func DelegatedCredentialSigningData(certificateDER, credentialDER []byte, algorithm SignatureScheme, role Role) ([]byte, error) {
	context, err := role.delegatedContext()
	if err != nil {
		return nil, err
	}
	if len(certificateDER) == 0 {
		return nil, fmt.Errorf("%w: empty certificate", ErrMalformedCredential)
	}
	if len(credentialDER) == 0 {
		return nil, fmt.Errorf("%w: empty credential", ErrMalformedCredential)
	}

	var out bytes.Buffer
	out.Write(bytes.Repeat([]byte{0x20}, 64))
	out.WriteString(context)
	out.WriteByte(0x00)
	out.Write(certificateDER)
	out.Write(credentialDER)
	out.Write([]byte{byte(algorithm >> 8), byte(algorithm)})
	return out.Bytes(), nil
}

// CertificateVerifySigningData constructs the TLS 1.3 CertificateVerify signed
// content. transcriptHash is the hash of the handshake transcript, not the raw
// transcript.
func CertificateVerifySigningData(transcriptHash []byte, role Role) ([]byte, error) {
	context, err := role.certificateVerifyContext()
	if err != nil {
		return nil, err
	}
	if len(transcriptHash) == 0 {
		return nil, fmt.Errorf("%w: empty transcript hash", ErrMalformedCredential)
	}

	var out bytes.Buffer
	out.Write(bytes.Repeat([]byte{0x20}, 64))
	out.WriteString(context)
	out.WriteByte(0x00)
	out.Write(transcriptHash)
	return out.Bytes(), nil
}

// Verifier validates delegated credentials and CertificateVerify signatures.
// The zero value is usable and uses the system clock and the seven-day RFC
// 9345 maximum validity.
type Verifier struct {
	// Now overrides the clock used for validity checks. If nil, time.Now is
	// used.
	Now func() time.Time
	// MaxValidity overrides the maximum credential lifetime. Zero uses the
	// RFC 9345 default of seven days. Values over seven days are rejected.
	MaxValidity time.Duration
}

// NewVerifier returns a verifier with the RFC 9345 defaults.
func NewVerifier() *Verifier {
	return &Verifier{}
}

func (v *Verifier) now() time.Time {
	if v != nil && v.Now != nil {
		return v.Now()
	}
	return time.Now()
}

func (v *Verifier) maxValidity() time.Duration {
	max := MaxDelegatedCredentialValidity
	if v != nil && v.MaxValidity > 0 && v.MaxValidity < max {
		max = v.MaxValidity
	}
	return max
}

// VerifyDelegatedCredential parses and validates a delegated credential
// against its delegation certificate. It does not publish a connection
// identity; that only happens after VerifyCertificateVerify succeeds.
func (v *Verifier) VerifyDelegatedCredential(cert *x509.Certificate, raw []byte, role Role) (*VerifiedDelegatedCredential, error) {
	dc, err := ParseDelegatedCredential(raw)
	if err != nil {
		return nil, err
	}
	return v.VerifyParsedDelegatedCredential(cert, dc, role)
}

// VerifyParsedDelegatedCredential validates an already-parsed credential.
func (v *Verifier) VerifyParsedDelegatedCredential(cert *x509.Certificate, dc *DelegatedCredential, role Role) (*VerifiedDelegatedCredential, error) {
	if !role.valid() {
		return nil, fmt.Errorf("%w: invalid role", ErrCertificateUsage)
	}
	if dc == nil {
		return nil, fmt.Errorf("%w: nil credential", ErrMalformedCredential)
	}
	if cert == nil {
		return nil, fmt.Errorf("%w: nil delegation certificate", ErrCertificateInvalid)
	}

	now := v.now()
	maxValidity := v.maxValidity()

	if err := checkDelegationCertificate(cert, role, now); err != nil {
		return nil, err
	}
	expiry, err := checkCredentialValidity(cert, dc, now, maxValidity)
	if err != nil {
		return nil, err
	}

	if !dc.CertVerifyAlgorithm.allowedForDelegatedKey() {
		return nil, fmt.Errorf("%w: %s is not allowed for a delegated key", ErrUnsupportedAlgorithm, dc.CertVerifyAlgorithm)
	}
	if err := checkKeySupportsScheme(dc.CertVerifyAlgorithm, dc.PublicKey, dc.publicKeyOID, dc.publicKeyPSSParms, false); err != nil {
		return nil, err
	}

	parentKey, parentOID, parentPSSParms, err := parseSubjectPublicKeyInfo(cert.RawSubjectPublicKeyInfo)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid delegation certificate public key: %v", ErrCertificateInvalid, err)
	}
	if err := checkKeySupportsScheme(dc.Algorithm, parentKey, parentOID, parentPSSParms, true); err != nil {
		return nil, err
	}

	signingData, err := DelegatedCredentialSigningData(cert.Raw, dc.rawCred, dc.Algorithm, role)
	if err != nil {
		return nil, err
	}
	if err := verifySignature(dc.Algorithm, parentKey, signingData, dc.Signature); err != nil {
		return nil, fmt.Errorf("delegated credential signature: %w", err)
	}

	return &VerifiedDelegatedCredential{
		certificate:         cert,
		role:                role,
		expires:             expiry,
		verifiedAt:          now,
		certVerifyAlgorithm: dc.CertVerifyAlgorithm,
		delegatedPublicKey:  dc.PublicKey,
	}, nil
}

// VerifyConnection performs both RFC 9345 verification and the subsequent TLS
// 1.3 CertificateVerify verification. It returns a non-nil identity only if
// every check succeeds.
func (v *Verifier) VerifyConnection(cert *x509.Certificate, raw []byte, role Role, transcriptHash []byte, certVerifyAlgorithm SignatureScheme, certVerifySignature []byte) (*ConnectionIdentity, error) {
	verified, err := v.VerifyDelegatedCredential(cert, raw, role)
	if err != nil {
		return nil, err
	}
	return verified.VerifyCertificateVerify(transcriptHash, certVerifyAlgorithm, certVerifySignature)
}

// VerifiedDelegatedCredential is the result of a successful delegated
// credential check. It deliberately does not expose a connection identity.
type VerifiedDelegatedCredential struct {
	certificate         *x509.Certificate
	role                Role
	expires             time.Time
	verifiedAt          time.Time
	certVerifyAlgorithm SignatureScheme
	delegatedPublicKey  crypto.PublicKey
}

// Expires returns the credential expiry time (certificate NotBefore plus
// valid_time).
func (v *VerifiedDelegatedCredential) Expires() time.Time {
	return v.expires
}

// VerifiedAt returns the time at which the credential was validated.
func (v *VerifiedDelegatedCredential) VerifiedAt() time.Time {
	return v.verifiedAt
}

// ExpectedCertificateVerifyAlgorithm returns the scheme required in the
// subsequent CertificateVerify message.
func (v *VerifiedDelegatedCredential) ExpectedCertificateVerifyAlgorithm() SignatureScheme {
	return v.certVerifyAlgorithm
}

// VerifyCertificateVerify verifies the peer's CertificateVerify signature
// using the delegated public key and the role-specific TLS 1.3 context. A
// connection identity is returned only when this succeeds.
func (v *VerifiedDelegatedCredential) VerifyCertificateVerify(transcriptHash []byte, certVerifyAlgorithm SignatureScheme, signature []byte) (*ConnectionIdentity, error) {
	if v == nil || v.certificate == nil || v.delegatedPublicKey == nil {
		return nil, fmt.Errorf("%w: unverified delegated credential", ErrMalformedCredential)
	}
	if !v.role.valid() {
		return nil, fmt.Errorf("%w: invalid role", ErrCertificateUsage)
	}
	if certVerifyAlgorithm != v.certVerifyAlgorithm {
		return nil, fmt.Errorf("%w: CertificateVerify uses %s, credential requires %s", ErrAlgorithmMismatch, certVerifyAlgorithm, v.certVerifyAlgorithm)
	}

	signingData, err := CertificateVerifySigningData(transcriptHash, v.role)
	if err != nil {
		return nil, err
	}
	if err := verifySignature(v.certVerifyAlgorithm, v.delegatedPublicKey, signingData, signature); err != nil {
		return nil, fmt.Errorf("CertificateVerify signature: %w", err)
	}

	return &ConnectionIdentity{
		Certificate:        v.certificate,
		DelegatedPublicKey: v.delegatedPublicKey,
		Expires:            v.expires,
		Role:               v.role,
	}, nil
}

// ConnectionIdentity is the authenticated peer identity. It is only produced
// after both the delegated credential and CertificateVerify signatures verify.
type ConnectionIdentity struct {
	Certificate        *x509.Certificate
	DelegatedPublicKey crypto.PublicKey
	Expires            time.Time
	Role               Role
}

func checkDelegationCertificate(cert *x509.Certificate, role Role, now time.Time) error {
	if now.Before(cert.NotBefore) || now.After(cert.NotAfter) {
		return fmt.Errorf("%w: certificate is not valid at %s", ErrCertificateInvalid, now.UTC().Format(time.RFC3339))
	}
	if cert.KeyUsage == 0 || cert.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		return fmt.Errorf("%w: certificate lacks digitalSignature KeyUsage", ErrCertificateUsage)
	}

	var delegationUsageCount int
	for _, extension := range cert.Extensions {
		if !extension.Id.Equal(oidDelegationUsage) {
			continue
		}
		delegationUsageCount++
		if extension.Critical {
			return fmt.Errorf("%w: DelegationUsage extension must not be critical", ErrCertificateUsage)
		}
		if !bytes.Equal(extension.Value, asn1.NullBytes) {
			return fmt.Errorf("%w: DelegationUsage extension is not ASN.1 NULL", ErrCertificateUsage)
		}
	}
	if delegationUsageCount == 0 {
		return fmt.Errorf("%w: certificate lacks DelegationUsage extension", ErrCertificateUsage)
	}
	if delegationUsageCount != 1 {
		return fmt.Errorf("%w: duplicate DelegationUsage extension", ErrCertificateUsage)
	}

	if len(cert.ExtKeyUsage) > 0 {
		allowed := false
		for _, usage := range cert.ExtKeyUsage {
			if usage == x509.ExtKeyUsageAny {
				allowed = true
				continue
			}
			if role == RoleServer && usage == x509.ExtKeyUsageServerAuth {
				allowed = true
			}
			if role == RoleClient && usage == x509.ExtKeyUsageClientAuth {
				allowed = true
			}
		}
		if !allowed {
			return fmt.Errorf("%w: certificate is not valid for %s authentication", ErrCertificateUsage, role)
		}
	}
	if len(cert.UnknownExtKeyUsage) > 0 {
		return fmt.Errorf("%w: certificate has an unknown extended key usage", ErrCertificateUsage)
	}

	return nil
}

func checkCredentialValidity(cert *x509.Certificate, dc *DelegatedCredential, now time.Time, maxValidity time.Duration) (time.Time, error) {
	validity := time.Duration(dc.ValidTime) * time.Second
	if validity > maxValidity {
		return time.Time{}, fmt.Errorf("%w: valid_time %s exceeds %s", ErrValidityTooLong, validity, maxValidity)
	}
	if cert.NotBefore.IsZero() || cert.NotAfter.IsZero() {
		return time.Time{}, fmt.Errorf("%w: certificate has no validity period", ErrCertificateInvalid)
	}
	if cert.NotAfter.Before(cert.NotBefore) {
		return time.Time{}, fmt.Errorf("%w: certificate validity period is inverted", ErrCertificateInvalid)
	}

	expiry := cert.NotBefore.Add(validity)
	if now.After(expiry) {
		return time.Time{}, fmt.Errorf("%w: credential expired at %s", ErrCredentialExpired, expiry.UTC().Format(time.RFC3339))
	}
	if expiry.After(now.Add(maxValidity)) {
		return time.Time{}, fmt.Errorf("%w: remaining validity extends past %s", ErrValidityTooLong, now.Add(maxValidity).UTC().Format(time.RFC3339))
	}
	// RFC 9345 requires expiry strictly before the certificate's NotAfter.
	if !expiry.Before(cert.NotAfter) {
		return time.Time{}, fmt.Errorf("%w: credential expiry is not inside certificate validity", ErrValidityTooLong)
	}
	return expiry, nil
}

var oidDelegationUsage = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 44363, 44}

type subjectPublicKeyInfo struct {
	Algorithm pkix.AlgorithmIdentifier
	PublicKey asn1.BitString
}

func parseSubjectPublicKeyInfo(der []byte) (crypto.PublicKey, asn1.ObjectIdentifier, *asn1.RawValue, error) {
	var info subjectPublicKeyInfo
	rest, err := asn1.Unmarshal(der, &info)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("%w: invalid SubjectPublicKeyInfo: %v", ErrMalformedCredential, err)
	}
	if len(rest) != 0 {
		return nil, nil, nil, fmt.Errorf("%w: trailing data in SubjectPublicKeyInfo", ErrMalformedCredential)
	}
	if info.PublicKey.BitLength%8 != 0 {
		return nil, nil, nil, fmt.Errorf("%w: public key bit string is not byte aligned", ErrMalformedCredential)
	}

	keyBytes := info.PublicKey.RightAlign()
	oid := info.Algorithm.Algorithm

	switch {
	case oid.Equal(oidRSAEncryption):
		if !bytes.Equal(info.Algorithm.Parameters.FullBytes, asn1.NullBytes) {
			return nil, nil, nil, fmt.Errorf("%w: rsaEncryption parameters must be NULL", ErrMalformedCredential)
		}
		key, err := x509.ParsePKCS1PublicKey(keyBytes)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("%w: invalid RSA public key: %v", ErrMalformedCredential, err)
		}
		return key, oid, nil, nil
	case oid.Equal(oidRSAPSS):
		params, err := parsePSSParameters(info.Algorithm.Parameters.FullBytes)
		if err != nil {
			return nil, nil, nil, err
		}
		key, err := x509.ParsePKCS1PublicKey(keyBytes)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("%w: invalid RSASSA-PSS public key: %v", ErrMalformedCredential, err)
		}
		return key, oid, params, nil
	default:
		key, err := x509.ParsePKIXPublicKey(der)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("%w: unsupported public key: %v", ErrMalformedCredential, err)
		}
		return key, oid, nil, nil
	}
}

func parsePSSParameters(der []byte) (*asn1.RawValue, error) {
	if len(der) == 0 {
		return nil, fmt.Errorf("%w: RSASSA-PSS key is missing parameters", ErrMalformedCredential)
	}
	var params asn1.RawValue
	rest, err := asn1.Unmarshal(der, &params)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid RSASSA-PSS parameters: %v", ErrMalformedCredential, err)
	}
	if len(rest) != 0 || params.Class != asn1.ClassUniversal || params.Tag != asn1.TagSequence {
		return nil, fmt.Errorf("%w: invalid RSASSA-PSS parameters", ErrMalformedCredential)
	}
	headerLen := len(params.FullBytes) - len(params.Bytes)
	fullBytes := append([]byte(nil), params.FullBytes...)
	params.FullBytes = fullBytes
	params.Bytes = fullBytes[headerLen:]
	return &params, nil
}

// VerifyDelegatedCredential is a convenience wrapper using the RFC 9345
// defaults and an explicit current time.
func VerifyDelegatedCredential(cert *x509.Certificate, raw []byte, role Role, now time.Time) (*VerifiedDelegatedCredential, error) {
	return (&Verifier{Now: func() time.Time { return now }}).VerifyDelegatedCredential(cert, raw, role)
}

// VerifyConnection is a convenience wrapper using the RFC 9345 defaults and an
// explicit current time. It never returns an identity on partial success.
func VerifyConnection(cert *x509.Certificate, raw []byte, role Role, now time.Time, transcriptHash []byte, certVerifyAlgorithm SignatureScheme, certVerifySignature []byte) (*ConnectionIdentity, error) {
	return (&Verifier{Now: func() time.Time { return now }}).VerifyConnection(cert, raw, role, transcriptHash, certVerifyAlgorithm, certVerifySignature)
}
