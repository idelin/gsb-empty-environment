// Package netsec implements trust-on-first-use (TOFU) certificate pinning
// for TLS clients.
//
// A pin is the SHA-256 hash of a certificate's SubjectPublicKeyInfo (SPKI),
// scoped to the hostname and port that the client connected to. The first
// successful handshake records the fingerprint, the first-seen time, and the
// certificate's expiry time. Later handshakes must present the same
// fingerprint, unless the stored certificate has already expired or the new
// certificate can be verified through a caller-supplied trust chain.
//
// The checks operate on a tls.ConnectionState and never record anything for
// a handshake that has not completed. All time comparisons use a time.Time
// supplied by the caller; a certificate is considered expired at its
// NotAfter instant.
package netsec
