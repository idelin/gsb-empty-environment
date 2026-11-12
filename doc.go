// Package netsec provides strict, per-listener ALPN allow-lists for Go TLS
// servers that share a certificate across application protocols.
//
// Use ConfigureTLS for handshake callbacks, and prefer NewListener so the TLS
// handshake completes before application code can write a protocol banner.
package netsec
