// Package netsec implements a reproducible SHA-256 prefix-MAC length-extension
// attack and the corresponding HMAC-SHA256 migration entry point.
//
// The attack targets verifiers that authenticate a request with
// SHA-256(secret || message) and accept arbitrary appended bytes as part of the
// message.  Given one valid message/tag pair, an attacker can try candidate
// secret lengths in [1, 64], append the original SHA-256 padding followed by an
// attacker-chosen suffix, and derive the new tag by importing the eight
// big-endian chaining words from the old tag.  The verifier callback exposes
// only (message, tag); it never reveals the secret.
//
// This demonstrates a protocol/authentication error confined to prefix MACs.  It
// does not find a SHA-256 collision, preimage, or second preimage.  The forged
// tag is the correct SHA-256 digest of secret || message || padding || suffix
// when the guessed secret length is right; the security failure is that the
// verifier treats unauthenticated bytes as part of the authenticated message.
//
// New request authentication should use VerifyHMACRequest or HMACVerifier,
// which compute HMAC-SHA256 over a canonical request whose method, path, and
// body are each bound by an unsigned 64-bit big-endian length prefix.  Tag
// comparison uses constant-time HMAC equality.
package netsec
