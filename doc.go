// Package taskpkg verifies prefix-complete, hash-chained audit records.
//
// A Record is a log entry signed with Ed25519. Its signature covers the
// device public key, the boot challenge, the sequence number, the previous
// record digest, and the record payload. The digest of a record therefore
// commits to the entire chain below it.
//
// A Seal is an Ed25519 signature over the final sequence number and the root
// digest of a chain. Verification never combines records from different
// devices or boot sessions, and it reports every observed fork instead of
// choosing one branch implicitly.
package taskpkg
