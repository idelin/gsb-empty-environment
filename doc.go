// Package dnssec validates DNSSEC RRSIG signatures and RFC 5155 NSEC3
// authenticated denial and wildcard proofs.
//
// The package is intentionally self-contained: it performs SHA-256,
// ECDSA-P256, SHA-1 NSEC3 iteration, canonical ordering, and proof validation
// without network access or external dependencies. Use Validator for a
// complete signature-plus-proof check, or NSEC3Set and the lower-level
// Validate* functions when signatures have already been authenticated.
package dnssec
