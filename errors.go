package netsec

import "errors"

var (
	// ErrMissingSignature means the request does not carry both
	// Signature-Input and Signature fields.
	ErrMissingSignature = errors.New("netsec: missing signature fields")

	// ErrMalformedSignature covers invalid Signature-Input or Signature
	// structured-field syntax.
	ErrMalformedSignature = errors.New("netsec: malformed signature")

	// ErrUnknownKey means the signature's keyid is not in the registry.
	ErrUnknownKey = errors.New("netsec: unknown key")

	// ErrUnsupportedAlgorithm means the registered algorithm is not
	// implemented by this package.
	ErrUnsupportedAlgorithm = errors.New("netsec: unsupported algorithm")

	// ErrAlgorithmMismatch means the optional alg parameter in the request
	// disagrees with the algorithm bound to the key in the registry.
	ErrAlgorithmMismatch = errors.New("netsec: signature algorithm does not match registered key")

	// ErrMissingCoveredComponent means one of the mandatory request
	// components is absent from the signature's covered component list.
	ErrMissingCoveredComponent = errors.New("netsec: missing covered component")

	// ErrContentDigest means Content-Digest is absent, malformed, or does
	// not match the SHA-256 digest recomputed from the request body.
	ErrContentDigest = errors.New("netsec: content digest verification failed")

	// ErrTimestamp means created is absent, malformed, or outside the
	// permitted clock-skew window.
	ErrTimestamp = errors.New("netsec: created timestamp is outside the permitted window")

	// ErrExpired means the signature's expires parameter is in the past.
	ErrExpired = errors.New("netsec: signature has expired")

	// ErrHeaderValue means a value used in the signature base contains a
	// carriage return or line feed.
	ErrHeaderValue = errors.New("netsec: component value contains CR or LF")

	// ErrInvalidSignature means the cryptographic signature check failed.
	ErrInvalidSignature = errors.New("netsec: invalid signature")

	// ErrInvalidKey means a key registered for an algorithm has the wrong
	// type or is otherwise unusable.
	ErrInvalidKey = errors.New("netsec: invalid key")

	// ErrDuplicateKey means a key ID was registered more than once.
	ErrDuplicateKey = errors.New("netsec: duplicate key ID")
)
