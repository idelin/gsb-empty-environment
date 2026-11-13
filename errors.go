package netsec

import "errors"

// Sentinel errors returned by the binding checks. Callers should use
// errors.Is so that additional context can be attached without losing the
// underlying reason.
var (
	// ErrMalformedCnf indicates that the token has a cnf claim that is not a
	// JSON object or contains no supported binding member.
	ErrMalformedCnf = errors.New("netsec: malformed cnf claim")

	// ErrCnfWithoutClientCertificate indicates that a certificate-bound
	// token was presented on a connection without a client certificate.
	ErrCnfWithoutClientCertificate = errors.New("netsec: token has cnf but the connection has no client certificate")

	// ErrCertificateBindingRequired indicates that the client is registered
	// as requiring certificate-bound tokens but the token is not bound.
	ErrCertificateBindingRequired = errors.New("netsec: client requires a certificate-bound token")

	// ErrInvalidFingerprint indicates that an x5t#S256 or jkt value is not a
	// canonical, unpadded base64url encoding of a SHA-256 digest.
	ErrInvalidFingerprint = errors.New("netsec: invalid SHA-256 base64url fingerprint")

	// ErrFingerprintMismatch indicates that the token fingerprint does not
	// match the certificate on the current connection.
	ErrFingerprintMismatch = errors.New("netsec: token fingerprint does not match the client certificate")

	// ErrJKTUnavailable indicates that the token has a jkt claim but the
	// connection has neither a JWK thumbprint nor a certificate from which
	// one can be derived.
	ErrJKTUnavailable = errors.New("netsec: token requires jkt but no client key thumbprint is available")

	// ErrJKTMismatch indicates that the token jkt does not match the client
	// key thumbprint for the current connection.
	ErrJKTMismatch = errors.New("netsec: token jkt does not match the client key thumbprint")

	// ErrNoClientCertificate indicates that a certificate-bound refresh or
	// endpoint operation was attempted without a client certificate.
	ErrNoClientCertificate = errors.New("netsec: no client certificate on connection")

	// ErrRotationNotGranted indicates that a refresh token is being redeemed
	// with a different certificate and no (old, new) rotation grant exists.
	ErrRotationNotGranted = errors.New("netsec: client certificate rotation is not authorized")

	// ErrCertificateAuthNotAllowed indicates that a client certificate was
	// presented at an endpoint that is not the mTLS token endpoint alias.
	ErrCertificateAuthNotAllowed = errors.New("netsec: client certificate authentication is not allowed at this endpoint")
)
