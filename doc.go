// Package netsec provides importable DNSSEC validation primitives focused on
// RRSIG wildcard-expansion detection.
//
// # Wildcard rule
//
// Label counts exclude the root label; for the RRset owner name a leading
// literal wildcard ("*") label is also excluded. Comparing an RRSIG's Labels
// field against that owner count yields three outcomes (see Classify):
//
//   - Labels > owner labels: a forgery. The signature cannot cover this owner.
//   - Labels == owner labels: an exact match; the owner name must not begin
//     with the "*" label.
//   - Labels < owner labels: wildcard expansion. The signed owner is rebuilt
//     as "*.<closest encloser>" and verification must also hold an NSEC proof
//     that the next-closer name (the wildcard itself) does not exist.
//
// The RRSIG Labels field itself always counts the leading "*" of a wildcard
// signer, so the closest encloser for Labels=L has L-1 labels.
//
// # Entry points
//
// Classify implements only the label decision and returns the rebuilt signed
// name. Verify additionally enforces the NSEC denial, validity period, DNSKEY
// matching and the cryptographic signature over the canonical (RFC 4034)
// signed data. VerifyNSECProof validates the closest-encloser/next-closer NSEC
// interval independently.
//
// Only the Go standard library is used; supported algorithms are
// RSA-SHA256/SHA512, ECDSA P-256/P-384 and Ed25519.
package netsec
