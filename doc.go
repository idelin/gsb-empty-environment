// Package netsec validates ARC (RFC 8617) chains at a receiving mail boundary.
//
// ValidateChain enforces continuous instance numbering, exactly one
// ARC-Seal, ARC-Message-Signature, and ARC-Authentication-Results per
// instance, and the allowed ARC-Seal cv values. Cryptographic verification is
// supplied by the caller so that this package can be used with any key store,
// message representation, and DKIM implementation.
package netsec
