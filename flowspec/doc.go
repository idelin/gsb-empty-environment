// Package flowspec implements an importable, offline FlowSpec control-plane
// safety toolkit for a Go routing security host.
//
// It provides three cooperating capabilities:
//
//  1. Binary decoding (and canonical re-encoding) of BGP FlowSpec NLRI as
//     defined by RFC 8955 (IPv4) and RFC 8956 (IPv6): all component types
//     1..12 (destination/source prefixes, protocol, port/destination-port/
//     source-port, ICMP type/code, TCP flags, packet length, DSCP and fragment
//     bits), the operator byte bits end-of-list (E), AND (A), NOT (N) and
//     match/equal (m) together with the greater-than/less-than relation bits
//     and the two-bit value length code, plus the FlowSpec extended-action
//     communities traffic-rate (0x8006), traffic-action (0x8007), redirect
//     (0x8008) and redirect-to-IP.
//
//  2. RPKI ROA origin/prefix admission. A rule's destination prefix is only
//     admitted when a valid ROA exists whose origin AS exactly matches the
//     announcing AS and whose length window satisfies
//     minLen <= prefixLen <= maxLength. Unauthorized-origin, not-found and
//     over-wide (blackhole) prefixes are rejected.
//
//  3. RFC 8955 style priority comparison and deterministic conflict solving:
//     overlap/coverage analysis detects a high-priority traffic-rate=0
//     (drop/blackhole) rule shadowing a lower-priority permit, cross-tenant
//     blackhole hijack and conflicting / cross-tenant redirect-to-IP actions.
//     Resolve classifies a concrete packet against the prioritized rule set.
//
// The package depends only on the Go standard library and never performs
// network access.
package flowspec
