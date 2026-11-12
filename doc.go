// Package netsec parses and verifies RFC 5755 attribute certificates.
//
// Verify authenticates the attribute authority signature and binds the
// certificate to a presented X.509 certificate using every Holder constraint
// present. Unknown critical extensions are rejected, and the validity period
// is treated as the half-open interval [notBefore, notAfter).
package netsec
