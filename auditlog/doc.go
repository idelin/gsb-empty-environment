// Package auditlog provides a tamper-evident, append-only audit log intended
// for security-sensitive Go services.
//
// # On-disk model
//
// The log is a directory of numbered segment files. A segment being written
// has the suffix .part; once finalized it is fsynced and atomically renamed
// to .log.
//
// Each record frame contains:
//   - magic, format version and flags,
//   - a monotonic 64-bit sequence number,
//   - the SHA-256 digest of the previous record (the hash chain),
//   - the payload length,
//   - the payload itself,
//   - an HMAC-SHA-256 over the sequence, length, predecessor digest and
//     payload, keyed by a symmetric HMAC key.
//
// Every sealed segment ends with an independent seal frame signed with
// Ed25519. The seal covers the segment index, record count, sequence range,
// the digest of the previous seal and the digest of the final record, so
// deleting, reordering or splicing segments is detectable.
//
// # Tear versus tamper
//
// A frame is "torn" when its bytes are physically incomplete at the end of
// an unsealed .part file, the signature of a power loss mid-write. Torn
// tails are reported in Report.TornTails, never silently truncated.
//
// A complete frame that fails HMAC or signature verification, a broken
// hash chain, a malformed frame, a missing seal or an invalid segment
// ordering is always returned as a *TamperError. The package never rewrites,
// truncates or deletes such evidence.
//
// # Recovery
//
// On Open, existing segments are fully verified. If a torn tail is present,
// the damaged .part is left byte-for-byte untouched and appends continue in
// a new segment whose first record chains from the last authenticated
// record. A Write or Sync failure freezes the Writer; callers must reopen
// rather than retry blindly.
//
// # Cryptography
//
// Only the Go standard library is used: crypto/hmac, crypto/sha256 and
// crypto/ed25519. Keys are supplied by the caller; GenerateKeys is a
// crypto/rand-backed convenience constructor.
package auditlog
