package netsec

import "errors"

// Verification errors. Callers can use errors.Is to distinguish them.
var (
	ErrMalformed = errors.New("netsec: malformed JWS")

	ErrMissingAlgorithm     = errors.New("netsec: missing alg header")
	ErrUnsupportedAlgorithm = errors.New("netsec: unsupported algorithm")
	ErrAlgorithmNotAllowed  = errors.New("netsec: algorithm not allowed")
	ErrInvalidKey           = errors.New("netsec: invalid key")
	ErrInvalidSignature     = errors.New("netsec: invalid signature")

	ErrUnsupportedCritical = errors.New("netsec: unsupported critical header")
	ErrNonCanonicalBase64  = errors.New("netsec: non-canonical base64url")

	ErrDetachedPayloadRequired = errors.New("netsec: detached payload was not declared")
	ErrDetachedNotDeclared     = errors.New("netsec: external payload supplied without detached mode")
	ErrEmbeddedPayload         = errors.New("netsec: detached token contains an embedded payload")

	ErrMissingContentType  = errors.New("netsec: missing content type hint")
	ErrContentTypeMismatch = errors.New("netsec: content type mismatch")
)
