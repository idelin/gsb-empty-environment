// Package netsec implements verification of signed, append-only log tree
// heads and guarded storage of blocks proven against those heads.
//
// Hashing follows RFC 9162: leaves are SHA-256(0x00 || data), interior nodes
// are SHA-256(0x01 || left || right), and an empty tree hashes to SHA-256 of
// the empty string.
//
// Signature verification is not built in. Callers inject a SignatureVerifier
// that authenticates the complete TreeHead, including its fork number.
package netsec
