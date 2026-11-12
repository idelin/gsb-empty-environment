// Package lsadb implements the OSPFv2 link-state advertisement (LSA)
// admission path that a routing process invokes when an LSA update is
// received from a neighbor.
//
// The package enforces four pieces of ingress control:
//
//   - Cryptographic authentication sequence numbers are tracked per neighbor
//     and replayed or regressing packets are rejected (RFC 2328 appendix D.1,
//     RFC 5709 section 4.0).
//   - LSAs that claim to be originated by the local router are never accepted
//     from the wire: when the received sequence number is not older than the
//     local instance the database re-originates a strictly newer instance
//     instead, and the counter-action is rate limited per LSA.
//   - LS sequence numbers are signed 32-bit values compared with RFC 2328
//     circular (wrap-around) semantics (RFC 2328 sections 12.1.2 and 13).
//   - A MaxAge LSA without a matching database instance is acknowledged and
//     discarded without creating an entry; a MaxAge LSA that matches an
//     instance flushes that instance (RFC 2328 section 13).
package lsadb
