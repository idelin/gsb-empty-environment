package netsec

import "errors"

var (
	// ErrInvalidPolicy reports an inconsistent or ambiguous namespace policy.
	ErrInvalidPolicy = errors.New("netsec: invalid source policy")

	// ErrInvalidResolverConfig reports an incomplete resolver configuration.
	ErrInvalidResolverConfig = errors.New("netsec: invalid resolver configuration")

	// ErrInvalidRequest reports a malformed module request.
	ErrInvalidRequest = errors.New("netsec: invalid module request")

	// ErrInvalidArtifact reports an artifact that cannot be authenticated.
	ErrInvalidArtifact = errors.New("netsec: invalid artifact")

	// ErrNoDeclaredSource means that no namespace binding covers the module.
	// Resolution stops without probing any repository.
	ErrNoDeclaredSource = errors.New("netsec: no declared source for module")

	// ErrSourceConflict means that an artifact claims to come from a source
	// other than the source pinned by the namespace policy.
	ErrSourceConflict = errors.New("netsec: source conflict")

	// ErrVersionMismatch means that the repository returned a version other
	// than the requested version.
	ErrVersionMismatch = errors.New("netsec: version mismatch")

	// ErrChecksumMismatch means that a checksum is different from the trusted
	// checksum, or does not match the downloaded content.
	ErrChecksumMismatch = errors.New("netsec: checksum mismatch")

	// ErrChecksumUnsupported means that the checksum algorithm is not recognized.
	ErrChecksumUnsupported = errors.New("netsec: unsupported checksum algorithm")

	// ErrSignatureMissing means that strict signature verification was requested
	// but the artifact has no signature.
	ErrSignatureMissing = errors.New("netsec: signature missing")

	// ErrSignatureInvalid means that the artifact signature did not verify.
	ErrSignatureInvalid = errors.New("netsec: signature invalid")

	// ErrUntrustedSigner means that the signer key is absent from the verifier's
	// trust set, or is not the signer required by the request.
	ErrUntrustedSigner = errors.New("netsec: untrusted signer")

	// ErrSignatureVerifierMissing means that signatures must be checked but no
	// verifier was configured.
	ErrSignatureVerifierMissing = errors.New("netsec: signature verifier missing")

	// ErrSourceUnavailable means that every repository bound to the pinned
	// source failed. Resolution never falls back to another source.
	ErrSourceUnavailable = errors.New("netsec: pinned source unavailable")

	// ErrInvalidCacheKey reports an incomplete cache key.
	ErrInvalidCacheKey = errors.New("netsec: invalid cache key")
)
