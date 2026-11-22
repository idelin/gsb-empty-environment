// Package fedpolicy projects a last-match-wins, allow/deny prefix policy
// from one federated identity directory into the positive prefix-and-label
// allow policy executable by a target host, without ever widening
// permission relative to any compatible old identity.
//
// Principals are qualified by a signed issuer domain, a tenant, an
// unbounded path of segments, and a finite label set. The tuple
// (issuer, tenant) is the naming authority: same-named tenants on different
// issuers never alias. Name mapping is supplied as finite-state transducers
// on bounded channels; nondeterministic arcs and multiple channels express
// many-to-one mappings, and cycles express unbounded paths.
//
// The entry point is Project. It adjudicates every input Candidate, returns
// a TargetPolicy that is the minimum equivalent set of positive allows, and
// attaches a Decision per candidate. Rejected candidates carry concrete
// Witness values: an old principal the old host actually denies (or that
// misses a required label) with its full mapping Trace, or a target identity
// with no old preimage. See PROOF.md for the decidability, soundness,
// coverage and minimality arguments.
package fedpolicy
