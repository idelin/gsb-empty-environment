// Package netsec validates WS-Security 1.1 headers of inbound SOAP messages.
//
// The package implements the application-level policy a SOAP gateway applies
// after XML signature verification:
//
//   - exactly one wsu:Timestamp inside the wsse:Security header, whose
//     wsu:Created is no more than MaxCreatedSkew in the future and whose
//     wsu:Expires is after wsu:Created, at most MaxTimestampTTL later, and
//     still in the future;
//   - globally unique wsu:Id attributes, with both the wsu:Timestamp and the
//     SOAP Body present exactly once and covered by the signed references
//     reported by the caller-injected SignatureVerifier;
//   - single-use enforcement of the UsernameToken (Nonce, Created) pair via a
//     pluggable NonceStore, keyed for the lifetime of the timestamp.
//
// Cryptographic XML-DSIG verification is intentionally out of scope: callers
// inject a SignatureVerifier built on their own signature stack, while this
// package owns the WS-Security policy around it.
package netsec
