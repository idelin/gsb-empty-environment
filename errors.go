package netsec

import "errors"

var (
	// ErrInvalidHeader reports a nil or otherwise unusable header map.
	ErrInvalidHeader = errors.New("netsec: invalid header")
	// ErrInvalidFieldName reports a field name that is not an HTTP token.
	ErrInvalidFieldName = errors.New("netsec: invalid field name")
	// ErrInvalidFieldValue reports a field value containing an invalid control
	// character.
	ErrInvalidFieldValue = errors.New("netsec: invalid field value")
	// ErrInvalidTrailerDeclaration reports a malformed Trailer header value.
	ErrInvalidTrailerDeclaration = errors.New("netsec: invalid trailer declaration")
	// ErrForbiddenTrailer reports an authentication, routing, or framing field
	// used in the trailer section.
	ErrForbiddenTrailer = errors.New("netsec: forbidden trailer field")
	// ErrUndeclaredTrailer reports a trailer that was not predeclared in the
	// authenticated header section.
	ErrUndeclaredTrailer = errors.New("netsec: undeclared trailer field")
	// ErrTrailerHeaderCollision reports a trailer whose name also occurs in the
	// authenticated header section. Merging it would overwrite authenticated
	// data, so it is rejected even when the values are identical.
	ErrTrailerHeaderCollision = errors.New("netsec: trailer collides with authenticated header field")
	// ErrMissingDeclaredTrailer reports a predeclared trailer that was not
	// delivered. This is the truncated-message case.
	ErrMissingDeclaredTrailer = errors.New("netsec: declared trailer field missing")
	// ErrNilDigest reports a missing digest callback.
	ErrNilDigest = errors.New("netsec: nil digest callback")
	// ErrEmptySignature reports an empty signature.
	ErrEmptySignature = errors.New("netsec: empty signature")
	// ErrInvalidSignatureEncoding reports a signature field that is not valid
	// base64.
	ErrInvalidSignatureEncoding = errors.New("netsec: invalid signature encoding")
	// ErrSignatureMismatch reports a signature that does not authenticate the
	// exact header and trailer field sequences.
	ErrSignatureMismatch = errors.New("netsec: signature mismatch")
)
