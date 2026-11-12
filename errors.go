package netsec

import "errors"

// Sentinel errors returned by the request object parser. Callers can use
// errors.Is to distinguish policy failures from ordinary transport failures.
var (
	ErrMissingRequest         = errors.New("request object is missing")
	ErrAmbiguousRequest       = errors.New("request and request_uri are both present")
	ErrInvalidRequestObject   = errors.New("invalid request object")
	ErrMalformedJOSE          = errors.New("malformed JOSE serialization")
	ErrDuplicateJSONKey       = errors.New("duplicate JSON member name")
	ErrUnsupportedJWSAlg      = errors.New("unsupported JWS algorithm")
	ErrAlgorithmNotAllowed    = errors.New("JOSE algorithm is not allowed")
	ErrSignatureInvalid       = errors.New("request object signature is invalid")
	ErrUnsupportedJWEAlg      = errors.New("unsupported JWE key management algorithm")
	ErrUnsupportedJWEEnc      = errors.New("unsupported JWE content encryption algorithm")
	ErrNestedJWE              = errors.New("more than one JWE layer is not allowed")
	ErrDecryptionFailed       = errors.New("request object decryption failed")
	ErrUnknownClient          = errors.New("unknown client")
	ErrMissingClientID        = errors.New("client_id is missing from the authorization request")
	ErrClientIDMismatch       = errors.New("client_id in the query does not match the request object")
	ErrIssuerMismatch         = errors.New("iss does not match client_id")
	ErrAudienceMismatch       = errors.New("aud does not contain the authorization server issuer")
	ErrNestedRequestParameter = errors.New("request object contains request or request_uri")
	ErrRequestURIUnavailable  = errors.New("request_uri cannot be resolved")
	ErrRequestURIFetch        = errors.New("request_uri fetch failed")
	ErrExpiredRequestObject   = errors.New("request object is outside its validity window")
	ErrInvalidSigningKey      = errors.New("no usable signing key is registered for the client")
	ErrInvalidDecryptionKey   = errors.New("no usable decryption key is configured")
)

func errorf(base error, detail string) error {
	if detail == "" {
		return base
	}
	return &wrappedError{base: base, detail: detail}
}

type wrappedError struct {
	base   error
	detail string
}

func (e *wrappedError) Error() string { return e.base.Error() + ": " + e.detail }
func (e *wrappedError) Unwrap() error { return e.base }
