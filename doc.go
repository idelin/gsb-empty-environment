// Package netsec implements the TLS 1.3 CertificateVerify signed input and
// signature helpers defined by RFC 8446, Section 4.4.3.
//
// A CertificateVerify signature covers:
//
//	64 * 0x20 || context label || 0x00 || Transcript-Hash
//
// The context label is "TLS 1.3, server CertificateVerify" for servers and
// "TLS 1.3, client CertificateVerify" for clients. The 64-space prefix is
// mandatory and is validated exactly; a supplied context missing even one
// space is rejected before signature verification.
package netsec
