// Package netsec implements hybrid KEM decapsulation for a handshake host.
//
// A hybrid suite binds two KEM adapters to a fixed suite ID. The host
// registers each suite together with the fixed canonical ciphertext length of
// both components. On every decapsulation both component shared secrets
// (including implicit-rejection secrets) are fed into a single HMAC-SHA256
// based key derivation that is bound to the suite ID, both length-prefixed
// ciphertexts, the canonical client and server public key digests, and the
// handshake context digest.
//
// A component failure never downgrades the derivation to the surviving
// component: both shared secrets are always used, the full key is always
// derived, and the derived key is released to the caller only when both
// components report validity. Otherwise the caller receives a generic,
// random-looking failure key of the same length and a single error that does
// not reveal which component failed. Internal diagnostics record only a bit
// mask of failing components.
//
// This package does not claim that the cryptographic implementations inside
// the host-registered adapters themselves have been validated. Adapters are
// host-supplied; the package only guarantees the composition rules, strict
// input handling, and transcript binding of the derived key.
package netsec
