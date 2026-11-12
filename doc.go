// Package netsec implements RFC 9101 request objects for a Go OAuth 2.0 /
// OpenID Connect authorization endpoint.
//
// The parser accepts a signed request object (compact JWS) or an encrypted
// request object whose plaintext is a compact JWS (JWE -> JWS). It verifies
// signatures and claims before returning anything, rejects nested request
// parameters, and ignores untrusted outer authorization query parameters.
package netsec
