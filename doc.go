// Package netsec implements LeaseSet2 admission control for netDb storage
// requests. It enforces timing and freshness rules (replay protection,
// publication skew, expiration and lease bounds, offline-signature
// lifetime) while leaving cryptographic signature verification to a
// caller-injected Verifier.
package netsec
