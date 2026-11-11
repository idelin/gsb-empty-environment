// Package netsec is a symbolic reachability auditor for finite directed
// network graphs whose nodes perform ordered packet filtering and source or
// destination interval translation (NAT).
//
// The auditor answers an audit question of the form: after every filter and
// address translation along every possible path, can a packet satisfying the
// ingress constraints ever reach the egress node while satisfying the egress
// constraints?
//
// # Semantics
//
// Each node applies its rules in two strictly ordered phases for every packet
// and every symbolic region:
//
//  1. Filters are evaluated first-match. The first rule whose field interval
//     contains the packet decides allow or drop; unmatched packets default to
//     allow.
//  2. Translations are then evaluated first-match. The first rule whose
//     target interval (and optional guard interval) contains the packet adds
//     a signed delta to the source or destination field; unmatched packets
//     pass through unchanged.
//
// Every translation is checked twice for integer overflow: a signed 64-bit
// pre-check on the addition, then a uint32 post-check on the result. An
// overflow drops the concrete packet, or the overflowing symbolic sub-region.
//
// # Symbolic execution and loops
//
// Symbolic packets are rectangles: the Cartesian product of a closed source
// interval and a closed destination interval over uint32. A chaotic-
// iteration worklist propagates rectangles through the graph. Cycles are
// handled by an interval-state fixpoint: a rectangle already covered by the
// accumulated state at a node is subsumed (and recorded as a LoopEvent)
// instead of being repropagated. Termination follows from the finite
// partition of uint32 x uint32 induced by all rule boundaries, with a
// configurable iteration ceiling as a defensive guard.
//
// # Cross-check and witnesses
//
// The symbolic result is cross-checked against exhaustive concrete
// enumeration on a narrowed address domain. The domain is seeded with every
// rule and constraint boundary (and full small intervals), then closed under
// every applicable translation delta, so no packet can ever be shifted to an
// address outside the domain. Set equality is checked in both directions at
// every node. Reachable findings are returned as Witness values carrying the
// original ingress packet, the exact node path and a per-hop trace; each
// witness can be replayed independently through Witness.Replay.
package netsec
