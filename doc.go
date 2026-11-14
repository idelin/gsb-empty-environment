// Package netsec implements deterministic QUIC connection-ID and stateless
// reset-token lifecycles for an edge gateway.
//
// A Manager handles one connection; a Gateway registers many managers under a
// shared CID/token tombstone ledger. Lifecycle operations return the audit
// witnesses that caused each transition, and Recover rebuilds state from an
// authenticated hash-chained journal guarded by a MAC-signed high-water
// checkpoint.
package netsec
