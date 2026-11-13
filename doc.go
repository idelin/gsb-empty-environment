// Package netsec computes and verifies TLS 1.3 PSK binders against the
// complete ClientHello transcript prefix up to each binder.
//
// For unkeyed transcript hashes each binder covers the ClientHello from its
// handshake-message start through the byte immediately before that binder's
// value. Keyed (HMAC) binders default to the RFC 8446 prefix ending at the
// pre_shared_key identities vector. Verification accepts both forms. Every
// offered identity is therefore authenticated, as is every extension that
// precedes pre_shared_key. No extension or identity may be omitted from the
// prefix.
//
// The package also accepts position-specific binders that include preceding
// binder entries, and supports an already-derived binder key for the standard
// HMAC construction. It has no non-standard-library dependencies and can be
// imported directly by a TLS stack.
package netsec
