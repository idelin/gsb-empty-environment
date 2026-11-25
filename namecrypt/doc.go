// Package namecrypt provides an encrypted directory-entry naming layer.
//
// A Directory binds an immutable Policy (directory id, 32-byte nonce, case
// mode and byte limits) and a master handle to a Store. The name key for a
// generation is
//
//	HMAC-SHA256(masterHandle, "name"||directoryID||keyGeneration||directoryNonce)
//
// and the on-disk bucket key is the first 20 bytes of
// HMAC-SHA256(nameKey, "lookup"||canonicalName). Each entry gets a fresh
// 16-byte nonce and its raw name is sealed with AES-256-GCM; the associated
// data binds the policy digest, canonical-name hash, inode id and inode
// generation, so every successful lookup decrypts and verifies inode identity.
//
// CaseFoldASCII folds only A-Z to a-z. Create atomically inspects every
// candidate sharing a lookup bucket: digest collisions with different
// canonical names coexist, while two distinct raw names mapping to one
// canonical name are rejected instead of overwriting a sensitive entry.
//
// Rename decrypts the source and reseals it under the destination policy; the
// source is removed only after the destination commits. RotateKey advances
// KeyGeneration and migrates entries in per-entry transactions that can be
// interrupted and resumed with ResumeRotation; during rotation both
// generations serve lookups, writes are refused, and old ciphertext is never
// reused under the new generation's key or lookup key.
package namecrypt
