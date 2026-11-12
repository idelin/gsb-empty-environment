// Package netsec validates JWT-SVID workload identity tokens for Go
// services.
//
// # Threat model
//
// Two token-misuse patterns are addressed explicitly:
//
//   - Cross-audience replay: a token minted for service A is replayed at
//     service B. Validation requires the token's "aud" claim to contain the
//     audience configured for this service; empty or missing audiences are
//     rejected.
//   - Federated key confusion: another trust domain signs a token carrying
//     the same SPIFFE ID string. The verification key is selected exclusively
//     from the bundle belonging to the trust domain named in "sub", so a
//     foreign key can never validate a local-domain identity.
//
// # Validation rules
//
// The subject ("sub") must be a valid SPIFFE ID: the literal "spiffe://"
// scheme, a lower-case trust domain without port or user info, no query or
// fragment, and a path without dot segments, empty segments or trailing
// slash. Only ES256, RS256 and PS256 signatures are accepted. "exp" and
// "iat" are mandatory; an "iat" more than 30 seconds in the future is
// rejected.
//
// The implementation depends only on the Go standard library.
package netsec
