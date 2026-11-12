// Package netsec verifies RFC 9261 Exported Authenticators for a TLS
// application. A Verifier is intended to be scoped to one connection and
// accepts the connection's key exporter on every Verify call. This makes an
// authenticator captured on another connection fail both the
// CertificateVerify and Finished checks.
//
// Authenticator requests are generated with an unpredictable
// certificate_request_context and are redeemed atomically, exactly once.
package netsec
