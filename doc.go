// Package netsec provides a small, importable state machine that validates the
// client side of TLS client authentication.
//
// A server CertificateRequest starts an exchange in which the client must send
// Certificate and CertificateVerify before Finished. Sending Finished directly
// after the request, or directly after Certificate, is rejected.
package netsec
