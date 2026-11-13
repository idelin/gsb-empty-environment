// Package netsec implements TLS certificate-signature scheme selection with
// correct handling of the signature_algorithms and signature_algorithms_cert
// extensions.
//
// The signature_algorithms_cert extension, when present, is the complete
// authority for signatures in certificates. When it is absent, the peer's
// signature_algorithms list is used instead. Selection always intersects the
// applicable peer list with the caller's locally supported list, so a scheme
// that neither side permits is never returned.
package netsec
