// Package netsec verifies TLS-style signatures while enforcing that the
// negotiated signature scheme is compatible with the end-entity public key.
//
// A mismatch is reported before any cryptographic verification is attempted.
// This prevents a certificate with, for example, an RSA public key from being
// paired with an ECDSA signature scheme.
package netsec
