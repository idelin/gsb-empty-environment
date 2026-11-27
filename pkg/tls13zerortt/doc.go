// Package tls13zerortt implements server-side TLS 1.3 (RFC 8446) 0-RTT
// early_data admission primitives: PSK binder verification, ticket age /
// parameter validation, per-ticket early-data accounting, and an exact or
// bounded anti-replay state machine with time-bucket rotation.
//
// The package is deliberately restricted to the Go standard library
// (crypto/hmac and crypto/sha256 plus encoding helpers); it performs no
// network I/O. A transport-security host wires its existing ticket store,
// request demux and record layer into the exported [Validator].
//
// Security model:
//
//   - PSK binders are recomputed over the truncated ClientHello bytes (the
//     handshake message with the pre_shared_key "binders" list removed and the
//     affected length fields rewritten), exactly as required by RFC 8446
//     Section 4.2.11.2, and compared with hmac.Equal (constant time).
//   - Replayed early_data never reaches application handling: a second
//     accepted-binder attempt for the same ticket identity is downgraded to a
//     1-RTT resumption handshake. Non-idempotent request paths can be forced
//     to reject 0-RTT via an idempotency policy.
//   - The bounded registry fails closed (reject early_data) when its state
//     cannot guarantee single-use semantics.
package tls13zerortt

// HashLength is the output length of SHA-256 in bytes. All primitives in this
// package are fixed to the SHA-256 hash, i.e. the TLS_AES_128_GCM_SHA256 and
// TLS_AES_256_GCM_SHA384 cipher suites; SHA-384 suites are out of scope and a
// 48-byte binder is rejected with ErrBinderLength.
const HashLength = 32
