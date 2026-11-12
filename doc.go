// Package netsec implements redacted audit-log exports with Merkle
// membership proofs.
//
// A log fixes an identifier and a contiguous sequence of records.  For every
// record the builder commits to its zero-based position, its sequence number
// and the SHA-256 digest of its payload.  The commitments are placed in a
// position-sensitive Merkle tree and the published root additionally binds the
// log identifier and the total number of records.
//
// An Export reveals payloads only for the caller-selected subset of records.
// Every other record contributes only its leaf commitment.  Verification
// requires the published (LogID, Root) trust anchor, checks that every
// position is present exactly once, validates the Merkle paths of public
// entries, and finally recomputes the root from all entries.  Consequently a
// deleted, reordered, duplicated or cross-log-spliced entry cannot verify.
package netsec
