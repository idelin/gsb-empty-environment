// Package netsec provides a transactional WebAuthn-style public-key
// registration flow for authentication hosts.
//
// The host supplies a [Verifier] callback that performs the device-attestation
// cryptography and returns the structured ceremony claims: purpose, origin
// (source), challenge, relying party id, credential id and public key. The
// [Service] checks that every claim matches the expected registration
// parameters and then occupies the credential id inside a single [Store]
// transaction, so a credential id can never be bound to two accounts or to two
// distinct public keys.
//
// Idempotency keys and natural credential-id replay both ensure that retries
// after a lost response return the original result instead of creating a
// second binding.
package netsec
