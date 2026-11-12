// Package netsec verifies RFC 9345 delegated credentials and the TLS 1.3
// CertificateVerify signature that follows them. It is intended to be called
// by a TLS terminator's handshake service and only releases an authenticated
// peer identity after every validity, certificate-usage, algorithm, context,
// and signature check succeeds.
package netsec
