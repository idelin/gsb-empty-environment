// Package netsec implements a P-256 / Ed25519 point-to-point handshake and a
// deliberately weak review target for an unknown-key-share exercise.
//
// Hardened mode binds a fixed domain label, the session ID, both role names,
// both registered Ed25519 public keys, X, and Y into a length-prefixed
// transcript. Both endpoints sign the SHA-256 transcript digest. The canonical
// compressed encoding of xyG is the HKDF-SHA256 input key material, the
// transcript digest is the salt, and the info labels send-A, send-B, and
// confirm derive the directional and confirmation keys. Role-separated HMAC
// confirmations bind the transcript and must be exchanged in the order A then
// B before traffic keys are released.
//
// Weak mode intentionally signs only X and Y and omits identities from the KDF
// salt. FindShortestIdentityConfusion returns a concrete unknown-key-share
// witness for weak mode; ProveHardenedRejects applies the same action set to
// hardened mode.
//
// Identity names are non-empty UTF-8 byte strings. A host-owned Registry maps
// each name to exactly one Ed25519 public key and each key to exactly one name.
// Handshake messages never carry public keys, so a router cannot substitute a
// key by rewriting an outer identity label.
package netsec
