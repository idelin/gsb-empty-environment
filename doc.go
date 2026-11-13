// Package netsec enforces Certificate Transparency log pinning for the TLS
// stack.
//
// A certificate is accepted only if at least one of its Signed Certificate
// Timestamps (SCTs) is issued by a log in the configured pinned set and its
// signature verifies over the certificate. SCTs from logs outside the pinned
// set never cause acceptance on their own; if no pinned SCT verifies,
// verification fails.
package netsec
