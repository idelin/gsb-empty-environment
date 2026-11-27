// Package dpop implements server-side verification of OAuth 2.0
// Demonstrating Proof of Possession (DPoP, RFC 9449) proof JWTs.
//
// The package only depends on the Go standard library and supports the
// asymmetric algorithms ES256 and EdDSA. It provides an importable
// net/http middleware that binds access tokens to the holder of a private
// key, prevents proof replay with a bounded jti cache, and manages rotating
// server-side DPoP-Nonce secrets.
package dpop

import (
	"errors"
	"net/http"
)

// RFC 9449 error codes used in WWW-Authenticate challenges.
const (
	ErrInvalidToken       = "invalid_token"
	ErrInvalidDPoP        = "invalid_dpop_proof"
	ErrUseDPoPNonce       = "use_dpop_nonce"
	ErrInvalidDPoPKeyBind = "invalid_dpop_key_binding"
)

// Error describes a failed DPoP verification step and carries the data the
// middleware needs to emit a standards-compliant WWW-Authenticate challenge.
type Error struct {
	// Code is one of the Err* constants and is used as the WWW-Authenticate
	// error parameter.
	Code string
	// Description is a short human-readable explanation used as the
	// error_description parameter.
	Description string
	// Nonce, when non-empty, is emitted as DPoP-Nonce on the response so that
	// the client can retry with a fresh server nonce.
	Nonce string
	// Status defaults to http.StatusUnauthorized.
	Status int
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Description == "" {
		return "dpop: " + e.Code
	}
	return "dpop: " + e.Code + ": " + e.Description
}

func newError(code, description string) *Error {
	return &Error{Code: code, Description: description, Status: http.StatusUnauthorized}
}

func nonceError(description, nonce string) *Error {
	return &Error{
		Code:        ErrUseDPoPNonce,
		Description: description,
		Nonce:       nonce,
		Status:      http.StatusUnauthorized,
	}
}

// AsError extracts a *Error from err, returning nil when err is nil or does
// not wrap a *Error.
func AsError(err error) *Error {
	var target *Error
	if errors.As(err, &target) {
		return target
	}
	return nil
}
