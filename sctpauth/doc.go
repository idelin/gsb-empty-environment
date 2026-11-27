// Package sctpauth implements the security-critical state machine of an
// SCTP (RFC 9260) endpoint: the four-way handshake State Cookie machinery and
// multihoming path validation.
//
// It is deliberately transport free: no sockets, packets never touch a wire
// and every operation is an in-memory function that the embedding host wires
// to its own SCTP stack. The only cryptographic primitives used are HMAC and
// SHA-256 from the standard library (crypto/hmac, crypto/sha256). Random
// nonces are produced by an HMAC-SHA256 DRBG seeded from the keying material.
//
// The package provides three guarantees:
//
//   - Stateless, tamper-evident INIT-ACK State Cookies authenticated with a
//     two-generation rotating key ring, binding both Verification Tags, both
//     initial TSNs, the peer address list, a creation time, lifespan and a
//     single-use nonce. Forgery, expiry, cross-source replay and illegal
//     Initiate Tags / Verification Tags are all rejected.
//   - Exact Verification Tag enforcement on every established-association
//     chunk, plus a strict sliding receive window on DATA chunk TSNs that
//     rejects off-path blind injection and replayed TSNs.
//   - A multihoming gating state machine in which secondary INIT addresses
//     and ASCONF-added addresses stay UNCONFIRMED until an independent
//     random HEARTBEAT/HEARTBEAT-ACK path proof succeeds. User data is never
//     emitted toward an unconfirmed destination, removing third-party
//     reflection/amplification redirection.
package sctpauth
