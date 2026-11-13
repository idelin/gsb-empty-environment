package netsec

import "errors"

var (
	// ErrMalformedToken indicates that the JWS could not be parsed safely.
	ErrMalformedToken = errors.New("netsec: malformed JWS token")

	// ErrInvalidSignature indicates that no signature on the token verified.
	ErrInvalidSignature = errors.New("netsec: invalid JWS signature")

	// ErrAlgorithmMissing indicates that the protected header has no alg value.
	ErrAlgorithmMissing = errors.New("netsec: protected header is missing alg")

	// ErrAlgorithmNotAllowed indicates that alg was rejected by policy.
	ErrAlgorithmNotAllowed = errors.New("netsec: algorithm not allowed")

	// ErrUnsupportedAlgorithm indicates that alg is unknown or unsafe.
	ErrUnsupportedAlgorithm = errors.New("netsec: unsupported algorithm")

	// ErrUnprotectedHeaderConflict indicates that a protected header parameter
	// has a different value in the unprotected header.
	ErrUnprotectedHeaderConflict = errors.New("netsec: protected and unprotected headers conflict")

	// ErrKeyNotFound indicates that the key resolver did not return a key.
	ErrKeyNotFound = errors.New("netsec: signing key not found")

	// ErrInvalidKey indicates that the selected key cannot be used with alg.
	ErrInvalidKey = errors.New("netsec: invalid signing key")

	// ErrKeyIDRequired indicates that policy requires a protected kid.
	ErrKeyIDRequired = errors.New("netsec: protected header is missing kid")

	// ErrDetachedPayload indicates that an external payload must be supplied.
	ErrDetachedPayload = errors.New("netsec: detached payload required")

	// ErrUnsupportedCritical indicates a crit or b64 extension that this
	// package does not implement.
	ErrUnsupportedCritical = errors.New("netsec: unsupported protected header extension")
)
