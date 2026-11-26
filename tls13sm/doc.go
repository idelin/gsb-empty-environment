// Package tls13sm implements a self-contained TLS 1.3 (RFC 8446) handshake and
// key-scheduling security state machine for high-frequency gateway hosts.
//
// The package deliberately does NOT import or invoke crypto/tls: it is built
// only on top of the basic cryptographic primitives crypto/sha256,
// crypto/sha512, crypto/hmac, crypto/aes, crypto/cipher and crypto/ecdh, and
// performs no network I/O of its own. Every wire interaction is expressed as
// explicit Record/handshake inputs and outputs so that callers stay in full
// control of their transport.
//
// Security controls enforced by the state machines:
//
//   - Full RFC 8446 section 7.1 key schedule (Early Secret -> Handshake
//     Secret -> Master Secret) with the exact HkdfLabel encoding of
//     HKDF-Expand-Label.
//   - HelloRetryRequest handling with the mandatory synthetic message_hash
//     transcript replacement; a second HRR and parameter-mismatching retries
//     are rejected.
//   - Downgrade protection: ServerHello.random[24:32] is checked against the
//     TLS 1.2 and TLS 1.1 sentinels of RFC 8446 section 4.1.3.
//   - 0-RTT Early Data three-line defense: ticket-age skew validation, a
//     single-use strike-register replay ledger and an explicit caller-supplied
//     idempotency declaration.
//   - Strict message ordering: application data before the handshake is
//     complete, plaintext Alert/KeyUpdate injection, missing EndOfEarlyData
//     and cross-epoch ciphertext are all rejected.
//   - Bounded KeyUpdate rotation with per-direction epoch tracking.
package tls13sm
