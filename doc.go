// Package netsec adds TLS 1.3 downgrade protection on top of crypto/tls.
//
// ServerHello downgrade sentinels are defined by RFC 8446, Section 4.1.3.
// TLS_FALLBACK_SCSV is defined by RFC 7507. The package exposes policy
// helpers, protected server/client connection constructors, a protected
// listener, and a client fallback dialer.
//
// The fallback dialer omits TLS_FALLBACK_SCSV from the first ClientHello and
// adds it only to retry ClientHellos. A server rejects such a retry with
// inappropriate_fallback when the client's legacy_version is below its
// maximum supported version.
package netsec
