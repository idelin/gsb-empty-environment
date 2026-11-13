// Package netsec implements the RFC 9146 tls12_cid record routing needed by a
// DTLS 1.2 server. It performs CID lookup, authenticated peer-address
// migration, a 64-record replay window, and path validation with an
// anti-amplification budget for the new address.
package netsec
