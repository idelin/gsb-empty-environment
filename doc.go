// Package netsec verifies RFC 9421 HTTP request signatures for Go gateways.
//
// The verifier binds each key ID to an algorithm in trusted key metadata,
// requires coverage of @method, @target-uri, and content-digest, requires a
// created parameter covered by the mandatory @signature-params signature-base
// line, recomputes the body's SHA-256 digest, enforces timestamp freshness and
// expiration, and rejects CR/LF injection while building the signature base.
package netsec
