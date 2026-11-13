package netsec

import "errors"

// Errors returned by this package. They are wrapped with additional context,
// so callers should use errors.Is rather than comparing error strings.
var (
	// ErrInvalidToken means the access token is not a JWT carrying valid claims.
	ErrInvalidToken = errors.New("dpop: invalid access token")
	// ErrTokenNotBound means the access token has no usable cnf.jkt claim.
	ErrTokenNotBound = errors.New("dpop: access token is not bound to a proof key")
	// ErrBindingMismatch means the proof key is not the key bound to the token.
	ErrBindingMismatch = errors.New("dpop: proof key does not match token cnf.jkt")

	// ErrProofRequired means no DPoP proof was supplied.
	ErrProofRequired = errors.New("dpop: proof is required")
	// ErrInvalidProof means the proof is not a well-formed or valid DPoP proof.
	ErrInvalidProof = errors.New("dpop: invalid proof")
	// ErrMissingJWK means the proof header does not contain a public JWK.
	ErrMissingJWK = errors.New("dpop: proof header is missing jwk")
	// ErrInvalidJWK means the proof JWK is malformed or contains private material.
	ErrInvalidJWK = errors.New("dpop: invalid jwk")
	// ErrUnsupportedAlgorithm means the proof uses an algorithm this package
	// cannot verify.
	ErrUnsupportedAlgorithm = errors.New("dpop: unsupported algorithm")
	// ErrInvalidSignature means the proof signature did not verify.
	ErrInvalidSignature = errors.New("dpop: invalid proof signature")

	// ErrProofExpired means the proof iat/exp is outside the accepted window.
	ErrProofExpired = errors.New("dpop: proof is expired or not yet valid")
	// ErrMethodMismatch means htm does not match the request method.
	ErrMethodMismatch = errors.New("dpop: htm does not match request method")
	// ErrURIMismatch means htu does not match the request URI.
	ErrURIMismatch = errors.New("dpop: htu does not match request URI")
	// ErrAccessTokenHashMismatch means the ath claim does not hash the access
	// token that was presented.
	ErrAccessTokenHashMismatch = errors.New("dpop: ath does not match access token")
	// ErrReplay means the proof jti has already been seen.
	ErrReplay = errors.New("dpop: proof has already been used")
)
