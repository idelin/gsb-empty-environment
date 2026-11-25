// Package handoff implements in-memory admission for safe partial tenant
// handoffs across independently converging route egresses.
//
// The package has no disk or network dependencies. Its central objects are:
//
//   - Spec: address domain, egress set, two tenants, transferred ranges,
//     always-reachable critical prefixes, and allowed-order bounds.
//   - Statement and Receipt: exact SHA-256 claims/batches and Ed25519-signed,
//     egress-bound acknowledgements. A receipt for one egress never satisfies
//     another egress, and an old digest/generation never satisfies a new one.
//   - View: one egress-local visible RIB version with longest-prefix matching,
//     exclusion holes, and equal-length cross-tenant conflict detection.
//   - Plan: an online ACK-barrier state machine produced by BuildSafePlan.
//   - Protocol and Certificate: a partial-order/bounded-window description and
//     an independently replayable exhaustive safety certificate.
//
// The default admission policy removes an unauthorized longer winner and then
// evaluates the next shorter prefix. This is equivalent to installing only
// authorized routes before ordinary longest-prefix lookup. It lets a new prefix
// be pre-positioned without attracting traffic before the authorization cut,
// while an old aggregate can continue to carry traffic until that cut. Equal
// length distinct-tenant matches are always hard conflicts and are never
// resolved by fallback.
package handoff
