// Package netsec validates OCSP stapling nonces for a TLS stack.
//
// A TLS client that sends an OCSP nonce must reject a stapled response unless
// the response echoes that exact nonce and is inside its freshness window.
package netsec
