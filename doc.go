// Package netsec verifies OpenSSH user and host certificate signatures.
//
// Verification is performed over the exact certificate prefix through the
// declared signing key. Critical options (including force-command),
// extensions, valid principals, and validity times are all inside that prefix,
// so changing any of them invalidates the CA signature.
package netsec
