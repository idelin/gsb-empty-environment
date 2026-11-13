// Package netsec verifies compact JWS tokens, including tokens whose payload
// is supplied separately (RFC 7515 detached content).
//
// Detached mode is never inferred. Callers must use VerifyDetached, or pass
// WithDetachedPayload / VerifyOptions{Detached: true, ...}. In detached mode
// the token's payload segment must be empty; a token that carries an embedded
// payload is rejected. Without detached mode, Verify always authenticates the
// token's own payload.
//
// When an external payload type is supplied, the protected header's cty (or
// an equivalent payload-type hint) must match it.
package netsec
