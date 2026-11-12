// Package netsec checks whether composed functions preserve caller-declared
// constant-time contracts.
//
// The host supplies a straight-line Program, a set of hand-constructed
// library Summary values, and per-call-site AllowedPublic declarations.
// Analyze propagates bit-level secret provenance through bitwise operations,
// arithmetic, selection, public masks, table lookups, branches, and summarized
// calls.  Findings contain the source input bit, the exact operation path, and
// the sink bit that violates the contract.
//
// Address sinks use raw provenance: marking bits public for timing (through
// CallSpec.AllowedPublic, ParamSummary.PublicBits, or an OpPublicMask) does not
// erase secret bits that still select a table entry or a summarized address.
// Set AllowPublicAddress explicitly to opt in to a looser contract.
//
// Execute and Differential provide a deterministic software model of the same
// IR for cross-checking findings.  They compare table addresses, branches, and
// summarized timing/address signatures.  This package reasons about software
// data flow only; it does not measure or claim to detect physical hardware
// leakage such as cache contention, speculative execution, or power side
// channels.  Values are limited to 64 bits; callers can split wider values at
// the host boundary.
package netsec
