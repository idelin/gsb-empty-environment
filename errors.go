package netsec

import "errors"

var (
	// ErrPolicyNotFound means the requested PolicyId is not declared by the
	// endpoint's UserTokenPolicies.
	ErrPolicyNotFound = errors.New("netsec: user token policy not found")

	// ErrPolicyAmbiguous means an endpoint declares the same PolicyId more than
	// once with conflicting token types or security policies.
	ErrPolicyAmbiguous = errors.New("netsec: user token policy is ambiguous")

	// ErrTokenTypeMismatch means the policy exists but does not allow the
	// requested token type.
	ErrTokenTypeMismatch = errors.New("netsec: token type is not allowed by the user token policy")

	// ErrPlaintextPassword means both the policy's effective security policy
	// and the password token's encryption algorithm are None.
	ErrPlaintextPassword = errors.New("netsec: password token encryption is required")

	// ErrEncryptionAlgorithmMismatch means the password token encryption
	// algorithm does not match the policy's effective security policy.
	ErrEncryptionAlgorithmMismatch = errors.New("netsec: password token encryption algorithm does not match policy")

	// ErrCertificateRequired means a certificate token did not contain a
	// certificate.
	ErrCertificateRequired = errors.New("netsec: certificate token certificate is required")

	// ErrCertificateInvalid means the token certificate could not be parsed.
	ErrCertificateInvalid = errors.New("netsec: certificate token certificate is invalid")

	// ErrCertificateMismatch means the token certificate differs from the
	// client certificate established by CreateSession.
	ErrCertificateMismatch = errors.New("netsec: certificate token does not match the session client certificate")

	// ErrServerCertificateMissing means the endpoint did not provide the
	// server certificate that must be covered by the client signature.
	ErrServerCertificateMissing = errors.New("netsec: endpoint server certificate is required")

	// ErrNonceMissing means a certificate token did not carry the server nonce
	// that must be covered by the client signature.
	ErrNonceMissing = errors.New("netsec: server nonce is required")

	// ErrSignatureRequired means a certificate token did not carry a
	// signature.
	ErrSignatureRequired = errors.New("netsec: certificate token signature is required")

	// ErrSignatureAlgorithmRequired means no signature algorithm was supplied
	// and none could be derived from the policy.
	ErrSignatureAlgorithmRequired = errors.New("netsec: certificate signature algorithm is required")

	// ErrSignatureAlgorithmUnsupported means the signature algorithm is
	// unknown or not usable with the token certificate.
	ErrSignatureAlgorithmUnsupported = errors.New("netsec: certificate signature algorithm is unsupported")

	// ErrSignatureAlgorithmMismatch means the explicit signature algorithm is
	// incompatible with the policy's expected signature algorithm.
	ErrSignatureAlgorithmMismatch = errors.New("netsec: certificate signature algorithm does not match policy")

	// ErrSignatureInvalid means the client signature did not verify over the
	// server certificate followed by the server nonce.
	ErrSignatureInvalid = errors.New("netsec: certificate token signature is invalid")

	// ErrNonceReused means the server nonce had already been consumed by a
	// successful certificate activation.
	ErrNonceReused = errors.New("netsec: server nonce has already been used")

	// ErrUnsupportedTokenType means the token type is declared but this
	// package cannot establish its identity.
	ErrUnsupportedTokenType = errors.New("netsec: user token type is not supported")
)
