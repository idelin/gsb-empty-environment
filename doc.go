// Package netsec defines the identity of a build action and the signed
// metadata used to decide whether a remote cache entry may be reused.
//
// The action identity covers every input that can change the bytes or the
// security meaning of a build output:
//
//   - the source tree root;
//   - the ordered command argument array;
//   - the canonical working directory;
//   - per-item environment variables, with secret values represented by a
//     host-HMAC version rather than plaintext;
//   - the toolchain artifact digest;
//   - dependency action output digests;
//   - platform attributes;
//   - the current security policy digest.
//
// All fields are encoded with a fixed tag and big-endian length. Mapping keys
// are sorted by raw UTF-8 bytes, and duplicate mapping keys are rejected.
// ActionKey is the SHA-256 of this canonical encoding.
//
// CacheEntry binds the output tree root, producer identity, action key,
// issuance time, output tree digest, and an Ed25519 signature. VerifyCacheHit
// recomputes the action inputs, verifies the signature, checks the producer
// authorization list, and hashes the output tree. Any missing field or
// mismatch is a cache miss; the package never falls back to a weaker key.
package netsec
