// Package encobj implements a local, field-level encrypted object store
// using only the Go standard library (crypto/aes + cipher.NewGCM).
//
// Objects are addressed by a stable object ID, carry a monotonic commit
// version, and contain a map of string field names to byte values. Every
// field ciphertext is bound by AEAD additional data to its object ID,
// stable field ID, field revision, schema version and key ID; a
// per-object AEAD-sealed manifest commits to the name->field-ID map, the
// live field set, every ciphertext digest and the commit version, so a
// single authentic field never proves object completeness.
//
// Durability: a shared 96-bit monotonic nonce allocator uses persistent
// high-water reservations (nonce.log); the commit protocol orders nonce
// reservation, field writes, manifest publication, index update and
// idempotency logging so that a crash at any boundary recovers either the
// old or the new complete object. See FORMAT.md for the on-disk layout
// and README.md for the security boundary.
package encobj
