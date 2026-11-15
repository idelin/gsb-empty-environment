// Package netsec implements tenant-isolated certificate compression for a Go
// TLS server (RFC 8879).
//
// The server only uses algorithms advertised by the client, enforces a
// tenant-scoped dictionary selection, caches by
// (algorithm, dictionary ID, SHA-256(certificate chain)), limits
// uncompressed_length to 2^24-1, verifies exact decompressed length as
// bad_certificate, sends at most one CompressedCertificate per connection, and
// falls back to an uncompressed Certificate when compression is not smaller.
package netsec
