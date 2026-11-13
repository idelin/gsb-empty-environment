// Package grant implements the importable grant state machine for a KMS host.
//
// # Security model
//
// A Grant binds a grantee principal to one key version, an operation set and
// context constraints (exact equality and an optional required subset). Its
// Digest is canonical and length-prefixed, so every field — including
// generation and Pending/Active/Retired status — is authenticated.
//
// Create persists a Pending record first, then invokes the host Authority to
// confirm the creator holds Grant permission on the key and every constraint.
// Activation mints grantToken = HMAC-SHA256(serviceKey, full digest || random
// tokenID || expiry). Pending tokens are usable only inside the caller's
// explicit, host-clock propagation window and only by the original creator
// with the original Create requestID.
//
// Authorize verifies the token MAC before touching state, then requires the
// same generation and an identical digest, a non-Retired lifecycle state,
// matching principal/operation/key/version and satisfied context constraints.
//
// Retire is restricted to the retiring principal or grantee and atomically
// increments the generation, changing the digest and invalidating every old
// token. Key rotation never extends a grant to the new version.
package grant
