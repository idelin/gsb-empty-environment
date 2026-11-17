package netsec

import "errors"

var (
	// ErrNoValidChain reports that no single delegation chain in the supplied
	// certificate set authorizes the request.
	ErrNoValidChain = errors.New("netsec: no valid delegation chain")

	// ErrInvalidRequest reports an incomplete or contradictory request.
	ErrInvalidRequest = errors.New("netsec: invalid request")

	// ErrInvalidCertificate reports a certificate that cannot be decoded or
	// whose fields are not well formed.
	ErrInvalidCertificate = errors.New("netsec: invalid certificate")

	// ErrTooManyCertificates reports a certificate pool larger than the
	// verifier's configured limit.
	ErrTooManyCertificates = errors.New("netsec: too many certificates")

	// ErrSignature reports an invalid Ed25519 signature.
	ErrSignature = errors.New("netsec: invalid signature")

	// ErrRevoked reports a certificate revoked by its (issuer, serial) pair.
	ErrRevoked = errors.New("netsec: certificate revoked")

	// ErrCycle reports a delegation path that repeats a subject key.
	ErrCycle = errors.New("netsec: cyclic delegation chain")
)

// Compatibility spellings used by callers that prefer shorter names.
var (
	ErrNoChain            = ErrNoValidChain
	ErrVerificationFailed = ErrNoValidChain
)
