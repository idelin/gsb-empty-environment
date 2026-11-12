// Package netsec provides a connection-level acknowledgment ledger for
// multipath transports.
//
// In a multipath connection packet numbers belong to one connection-level
// space, while every transmission additionally remembers the path instance it
// used and the frame references it carried. Mixing per-path acknowledgment
// spaces would let an ACK arriving on one path release retransmitted data
// owned by another path. Ledger keeps a single authoritative space instead:
//
//   - Sent records a transmitted packet with its path and frame references.
//   - AckRanges only succeeds when every acknowledged number has a live send
//     record and the carrying path is authenticated; otherwise the whole ACK
//     is rejected with no side effects.
//   - Each frame reference is released at most once, so duplicate ACKs and
//     frames carried by several packets are handled idempotently.
//   - RetireAcked frees acknowledged slots; packet-number wrap is only
//     permitted once no in-flight record can alias the reused slot.
//
// The intervalSet type is the interval-reference model backing the ledger and
// its tests.
package netsec
