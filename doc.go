// Package netsec implements the server side of RFC 8879 certificate
// compression for a Go TLS handshake path.
//
// It provides strict CompressedCertificate framing, a zlib codec (with an
// extensible registry for other RFC 8879 algorithms), tenant-scoped
// dictionaries selected by SNI, a SHA-256 keyed compression cache, and
// per-connection one-shot negotiation.
package netsec
