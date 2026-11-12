// Package netsec implements pre-decryption validation for inbound JWE
// compact-serialization tokens at a Go message gateway.
//
// The attacker controls the JWE protected header (alg, enc, epk, zip and the
// JSON structure itself). ValidateCompactJWE must therefore run before any key
// unwrapping, ECDH agreement, decompression or content decryption, and reject:
//
//   - alg / enc values outside the gateway allow tables;
//   - key-wrapping algorithms paired with an enc whose CEK length does not
//     match the wrapped key length;
//   - ECDH-ES headers whose epk is not a valid P-256 curve point;
//   - any zip header (compression bombs);
//   - duplicate JSON header fields, including duplicates nested inside epk;
//   - structurally malformed compact tokens.
//
// Validation performs no cryptographic key agreement and requires no private
// key material, so every rejection provably happens before decryption.
package netsec
