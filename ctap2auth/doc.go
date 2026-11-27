// Package ctap2auth implements the CTAP 2.1 PIN/UV Auth Protocol Two
// (ClientPIN, pinUvAuthProtocol=2) and the credProtect extension policy engine
// for a Go authenticator host / passwordless middleware.
//
// It depends only on the Go standard library. No network access is required or
// performed.
//
// # Protocol Two primitives
//
// P-256 ECDH is provided by crypto/ecdh. Peer keyAgreement points pass full
// NIST SP 800-56A public-key validation, so non-curve, point-at-infinity and
// low-order/small-subgroup encodings are rejected before any shared secret is
// used.
//
// Two independent symmetric keys are derived from the 32-byte ECDH output Z:
//
//	hmacKey = HKDF-SHA-256(Z, salt=0x00*32, info="CTAP2 HMAC key", L=32)
//	aesKey  = HKDF-SHA-256(Z, salt=0x00*32, info="CTAP2 AES key",  L=32)
//
// Confidentiality is AES-256-CBC with a mandatory 16-byte random IV (the IV is
// prepended to the ciphertext); all-zero IVs are rejected on both encrypt and
// decrypt. Integrity is the full 32-byte HMAC-SHA-256. A 16-byte tag, which is
// the truncated MAC of PIN/UV Auth Protocol One, is hard rejected so a protocol
// downgrade cannot be forced through the Protocol Two verifier.
//
// # PIN retry and lock state machine
//
// PINManager maintains the pinRetries budget (CTAP2 default 8), the power-up
// scoped three-strike pinUvAuthParam counter (CTAP2_ERR_PIN_AUTH_BLOCKED) and
// the permanent zero-retry lock (CTAP2_ERR_PIN_BLOCKED). A power cycle clears
// the volatile auth block and tokens but never revives a permanently blocked
// PIN. pinUvAuthTokens carry an mc/ga/cm/be permission bitmask; makeCredential
// and getAssertion tokens are bound to an RP ID and cannot be replayed against
// another site.
//
// # credProtect
//
// Level 1 is returned whenever the RP matches; level 2 requires an allowList
// entry or a verified user when no allowList is present; level 3 always
// requires a verified user regardless of the allowList. FilterDiscoverableCredentials
// drops every unauthorized credential so that discoverable (resident)
// credential metadata cannot be silently enumerated without UV.
package ctap2auth
