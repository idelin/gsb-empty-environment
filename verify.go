package netsec

import (
	"bytes"
	"crypto/sha1"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"fmt"
	"time"
)

var (
	oidMD5WithRSA      = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 4}
	oidSHA1WithRSA     = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 5}
	oidSHA256WithRSA   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 11}
	oidSHA384WithRSA   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 12}
	oidSHA512WithRSA   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 13}
	oidRSAPSS          = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 10}
	oidDSAWithSHA1     = asn1.ObjectIdentifier{1, 2, 840, 10040, 4, 3}
	oidDSAWithSHA256   = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 3, 2}
	oidECDSAWithSHA1   = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 1}
	oidECDSAWithSHA256 = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 2}
	oidECDSAWithSHA384 = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 3}
	oidECDSAWithSHA512 = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 4}
	oidEd25519         = asn1.ObjectIdentifier{1, 3, 101, 112}
	oidMGF1            = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 8}
)

// VerifyOptions controls response timestamp and signature checking.
type VerifyOptions struct {
	// Now is the verification time. The zero value means time.Now().
	Now time.Time
	// ClockSkew allows for small timestamp differences between systems.
	// The zero value means five minutes.
	ClockSkew time.Duration
	// SkipFreshness disables thisUpdate/nextUpdate/producedAt checks. It does
	// not disable signature verification.
	SkipFreshness bool
}

// VerifyResponse parses an OCSP response, verifies that it was signed by the
// issuer or an authorized delegated responder, checks freshness, and returns
// the status for cert. Conflicting duplicate SingleResponse entries are
// rejected before a status is returned.
func VerifyResponse(der []byte, cert, issuer *x509.Certificate) (*Response, error) {
	return VerifyResponseWithOptions(der, cert, issuer, VerifyOptions{})
}

// VerifyResponseForCert is an alias for VerifyResponse.
func VerifyResponseForCert(der []byte, cert, issuer *x509.Certificate) (*Response, error) {
	return VerifyResponse(der, cert, issuer)
}

// VerifyResponseWithOptions is like VerifyResponse but allows the caller to
// supply the verification time and clock-skew policy.
func VerifyResponseWithOptions(der []byte, cert, issuer *x509.Certificate, options VerifyOptions) (*Response, error) {
	if cert == nil || issuer == nil || cert.SerialNumber == nil {
		return nil, fmt.Errorf("%w: certificate and issuer are required", ErrMalformedResponse)
	}

	basic, err := ParseBasicResponse(der)
	if err != nil {
		return nil, err
	}
	signer, err := basic.VerifySignature(issuer)
	if err != nil {
		return nil, err
	}
	matches, err := matchingResponses(basic.Responses, cert, issuer)
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("%w: serial %v", ErrCertificateNotFound, cert.SerialNumber)
	}
	response, err := responseForMatches(basic, matches, cert.SerialNumber)
	if err != nil {
		return nil, err
	}
	response.Certificate = signer

	if !options.SkipFreshness {
		if err := checkFreshness(basic, matches, options); err != nil {
			return nil, err
		}
	}
	return response, nil
}

// VerifySignature verifies the BasicOCSPResponse signature. The signer must
// be the issuer itself or a delegated responder certificate signed by the
// issuer and carrying the id-kp-OCSPSigning extended key usage.
func (b *BasicResponse) VerifySignature(issuer *x509.Certificate) (*x509.Certificate, error) {
	if b == nil {
		return nil, fmt.Errorf("%w: nil response", ErrMalformedResponse)
	}
	if issuer == nil {
		return nil, fmt.Errorf("%w: issuer is required", ErrMalformedResponse)
	}
	if len(b.tbsDER) == 0 || b.signature.BitLength == 0 {
		return nil, fmt.Errorf("%w: missing signature", ErrInvalidSignature)
	}
	if b.signature.BitLength != len(b.signature.Bytes)*8 {
		return nil, fmt.Errorf("%w: signature has unused bits", ErrInvalidSignature)
	}
	if len(b.ResponderID.ByName) == 0 && len(b.ResponderID.ByKey) == 0 {
		return nil, fmt.Errorf("%w: missing responderID", ErrMalformedResponse)
	}
	if len(b.ResponderID.ByName) != 0 && len(b.ResponderID.ByKey) != 0 {
		return nil, fmt.Errorf("%w: responderID has multiple forms", ErrMalformedResponse)
	}

	signatureAlgorithm, err := signatureAlgorithmFor(b.signatureAlgorithm)
	if err != nil {
		return nil, err
	}

	candidates := append([]*x509.Certificate(nil), b.Certificates...)
	candidates = append(candidates, issuer)
	var lastSignatureError error
	for _, candidate := range candidates {
		if candidate == nil {
			continue
		}
		if err := responderIDMatches(b.ResponderID, candidate); err != nil {
			if len(b.Certificates) == 0 {
				return nil, err
			}
			continue
		}
		if err := trustedResponder(candidate, issuer, b.ProducedAt); err != nil {
			continue
		}
		if err := candidate.CheckSignature(signatureAlgorithm, b.tbsDER, b.signature.Bytes); err != nil {
			lastSignatureError = err
			continue
		}
		return candidate, nil
	}

	if lastSignatureError != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSignature, lastSignatureError)
	}
	return nil, ErrUntrustedResponder
}

func responderIDMatches(id ResponderID, cert *x509.Certificate) error {
	switch {
	case len(id.ByName) != 0:
		if !bytes.Equal(id.ByName, cert.RawSubject) {
			return fmt.Errorf("%w: responder name does not match signer", ErrUntrustedResponder)
		}
		return nil
	case len(id.ByKey) != 0:
		var spki subjectPublicKeyInfo
		rest, err := asn1.Unmarshal(cert.RawSubjectPublicKeyInfo, &spki)
		if err != nil || len(rest) != 0 {
			return fmt.Errorf("%w: invalid responder public key", ErrUntrustedResponder)
		}
		keyHash := sha1.Sum(spki.SubjectPublicKey.Bytes)
		if !bytes.Equal(id.ByKey, keyHash[:]) {
			return fmt.Errorf("%w: responder key does not match signer", ErrUntrustedResponder)
		}
		return nil
	default:
		return fmt.Errorf("%w: empty responderID", ErrMalformedResponse)
	}
}

func trustedResponder(candidate, issuer *x509.Certificate, producedAt time.Time) error {
	if producedAt.IsZero() {
		return fmt.Errorf("%w: missing producedAt", ErrMalformedResponse)
	}
	if producedAt.Before(candidate.NotBefore) || producedAt.After(candidate.NotAfter) {
		return fmt.Errorf("%w: responder certificate is not valid at producedAt", ErrUntrustedResponder)
	}
	if len(candidate.UnhandledCriticalExtensions) != 0 {
		return fmt.Errorf("%w: responder certificate has unhandled critical extensions", ErrUntrustedResponder)
	}

	if candidate.Equal(issuer) {
		return nil
	}
	if err := candidate.CheckSignatureFrom(issuer); err != nil {
		return fmt.Errorf("%w: %v", ErrUntrustedResponder, err)
	}
	if !hasOCSPSigningEKU(candidate) {
		return fmt.Errorf("%w: delegated responder lacks id-kp-OCSPSigning", ErrUntrustedResponder)
	}
	return nil
}

func hasOCSPSigningEKU(cert *x509.Certificate) bool {
	for _, usage := range cert.ExtKeyUsage {
		if usage == x509.ExtKeyUsageOCSPSigning || usage == x509.ExtKeyUsageAny {
			return true
		}
	}
	return false
}

func signatureAlgorithmFor(algorithm pkix.AlgorithmIdentifier) (x509.SignatureAlgorithm, error) {
	switch {
	case algorithm.Algorithm.Equal(oidMD5WithRSA):
		return x509.MD5WithRSA, nil
	case algorithm.Algorithm.Equal(oidSHA1WithRSA):
		return x509.SHA1WithRSA, nil
	case algorithm.Algorithm.Equal(oidSHA256WithRSA):
		return x509.SHA256WithRSA, nil
	case algorithm.Algorithm.Equal(oidSHA384WithRSA):
		return x509.SHA384WithRSA, nil
	case algorithm.Algorithm.Equal(oidSHA512WithRSA):
		return x509.SHA512WithRSA, nil
	case algorithm.Algorithm.Equal(oidDSAWithSHA1):
		return x509.DSAWithSHA1, nil
	case algorithm.Algorithm.Equal(oidDSAWithSHA256):
		return x509.DSAWithSHA256, nil
	case algorithm.Algorithm.Equal(oidECDSAWithSHA1):
		return x509.ECDSAWithSHA1, nil
	case algorithm.Algorithm.Equal(oidECDSAWithSHA256):
		return x509.ECDSAWithSHA256, nil
	case algorithm.Algorithm.Equal(oidECDSAWithSHA384):
		return x509.ECDSAWithSHA384, nil
	case algorithm.Algorithm.Equal(oidECDSAWithSHA512):
		return x509.ECDSAWithSHA512, nil
	case algorithm.Algorithm.Equal(oidEd25519):
		return x509.PureEd25519, nil
	case algorithm.Algorithm.Equal(oidRSAPSS):
		return pssSignatureAlgorithm(algorithm.Parameters)
	default:
		return x509.UnknownSignatureAlgorithm, fmt.Errorf("%w: %v", ErrUnsupportedSignatureAlgorithm, algorithm.Algorithm)
	}
}

type pssParameters struct {
	HashAlgorithm    pkix.AlgorithmIdentifier `asn1:"explicit,optional,tag:0"`
	MaskGenAlgorithm pkix.AlgorithmIdentifier `asn1:"explicit,optional,tag:1"`
	SaltLength       int                      `asn1:"explicit,optional,tag:2,default:20"`
	TrailerField     int                      `asn1:"explicit,optional,tag:3,default:1"`
}

func pssSignatureAlgorithm(parameters asn1.RawValue) (x509.SignatureAlgorithm, error) {
	var params pssParameters
	if len(parameters.FullBytes) == 0 {
		return x509.UnknownSignatureAlgorithm, fmt.Errorf("%w: RSASSA-PSS parameters are missing", ErrUnsupportedSignatureAlgorithm)
	}
	rest, err := asn1.Unmarshal(parameters.FullBytes, &params)
	if err != nil || len(rest) != 0 {
		return x509.UnknownSignatureAlgorithm, fmt.Errorf("%w: invalid RSASSA-PSS parameters", ErrUnsupportedSignatureAlgorithm)
	}
	if len(params.MaskGenAlgorithm.Algorithm) != 0 && !params.MaskGenAlgorithm.Algorithm.Equal(oidMGF1) {
		return x509.UnknownSignatureAlgorithm, fmt.Errorf("%w: unsupported RSASSA-PSS mask", ErrUnsupportedSignatureAlgorithm)
	}

	switch {
	case params.HashAlgorithm.Algorithm.Equal(oidSHA256):
		return x509.SHA256WithRSAPSS, nil
	case params.HashAlgorithm.Algorithm.Equal(oidSHA384):
		return x509.SHA384WithRSAPSS, nil
	case params.HashAlgorithm.Algorithm.Equal(oidSHA512):
		return x509.SHA512WithRSAPSS, nil
	default:
		return x509.UnknownSignatureAlgorithm, fmt.Errorf("%w: unsupported RSASSA-PSS hash %v", ErrUnsupportedSignatureAlgorithm, params.HashAlgorithm.Algorithm)
	}
}

func checkFreshness(basic *BasicResponse, matches []SingleResponse, options VerifyOptions) error {
	now := options.Now
	if now.IsZero() {
		now = time.Now()
	}
	skew := options.ClockSkew
	if skew == 0 {
		skew = 5 * time.Minute
	}
	latestAllowed := now.Add(skew)

	if basic.ProducedAt.IsZero() {
		return fmt.Errorf("%w: missing producedAt", ErrMalformedResponse)
	}
	if basic.ProducedAt.After(latestAllowed) {
		return fmt.Errorf("%w: producedAt is in the future", ErrInvalidResponseTime)
	}
	for _, match := range matches {
		if match.ThisUpdate.After(latestAllowed) {
			return fmt.Errorf("%w: thisUpdate is in the future", ErrInvalidResponseTime)
		}
		if !match.NextUpdate.IsZero() && now.After(match.NextUpdate.Add(skew)) {
			return fmt.Errorf("%w: nextUpdate passed at %s", ErrResponseExpired, match.NextUpdate.UTC().Format(time.RFC3339))
		}
		if match.Status == Revoked && !match.RevokedAt.IsZero() && match.RevokedAt.After(latestAllowed) {
			return fmt.Errorf("%w: revocation time is in the future", ErrInvalidResponseTime)
		}
	}
	return nil
}

// CheckStatus performs full verification and returns the certificate status.
// Revoked is returned as a status, not as an error, so path validation code
// must decide how to handle revocation. Use ValidateCertificateStatus for a
// fail-closed policy.
func CheckStatus(der []byte, cert, issuer *x509.Certificate) (int, error) {
	response, err := VerifyResponse(der, cert, issuer)
	if err != nil {
		return Unknown, err
	}
	return response.Status, nil
}

// ValidateResponse performs full verification and applies a fail-closed
// policy: revoked certificates return a *RevokedError, unknown certificates
// return ErrUnknownStatus, and conflicting responses return
// ErrConflictingStatus.
func ValidateResponse(der []byte, cert, issuer *x509.Certificate) error {
	response, err := VerifyResponse(der, cert, issuer)
	if err != nil {
		return err
	}
	switch response.Status {
	case Good:
		return nil
	case Revoked:
		return &RevokedError{
			SerialNumber:     response.SerialNumber,
			RevokedAt:        response.RevokedAt,
			RevocationReason: response.RevocationReason,
		}
	case Unknown:
		return ErrUnknownStatus
	default:
		return fmt.Errorf("%w: status %d", ErrMalformedResponse, response.Status)
	}
}

// ValidateResponseForCert is an alias for ValidateResponse.
func ValidateResponseForCert(der []byte, cert, issuer *x509.Certificate) error {
	return ValidateResponse(der, cert, issuer)
}

// CheckResponse is an alias for ValidateResponse.
func CheckResponse(der []byte, cert, issuer *x509.Certificate) error {
	return ValidateResponse(der, cert, issuer)
}

// CheckCertificateStatus is the same as CheckStatus with the conventional
// certificate-first argument order used by many path-validation callers.
func CheckCertificateStatus(cert, issuer *x509.Certificate, der []byte) (int, error) {
	return CheckStatus(der, cert, issuer)
}

// StatusForCertificate is an alias for CheckCertificateStatus.
func StatusForCertificate(cert, issuer *x509.Certificate, der []byte) (int, error) {
	return CheckCertificateStatus(cert, issuer, der)
}

// ValidateCertificateStatus applies the fail-closed validation policy.
func ValidateCertificateStatus(cert, issuer *x509.Certificate, der []byte) error {
	return ValidateResponse(der, cert, issuer)
}

// VerifyCertificateStatus returns the verified Response for cert.
func VerifyCertificateStatus(cert, issuer *x509.Certificate, der []byte) (*Response, error) {
	return VerifyResponse(der, cert, issuer)
}

// VerifyOCSPResponse is an alias for VerifyResponse.
func VerifyOCSPResponse(der []byte, cert, issuer *x509.Certificate) (*Response, error) {
	return VerifyResponse(der, cert, issuer)
}

// ValidateOCSPResponse is an alias for ValidateResponse.
func ValidateOCSPResponse(der []byte, cert, issuer *x509.Certificate) error {
	return ValidateResponse(der, cert, issuer)
}

// CheckOCSPResponse returns the verified status. Revoked is a status, not an
// error.
func CheckOCSPResponse(der []byte, cert, issuer *x509.Certificate) (int, error) {
	return CheckStatus(der, cert, issuer)
}
