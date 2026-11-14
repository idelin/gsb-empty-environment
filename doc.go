// Package netsec provides an importable resource-pattern authorization
// component for a multi-tenant Go message broker.
//
// # Resource model
//
// Topics, consumer groups and transaction ids are each modeled as a typed
// Resource and authorized through typed Pattern values:
//
//   - TypeLiteral matches one exact, case sensitive name.
//   - TypePrefix matches the exact name or a child separated by the
//     configured delimiter; a trailing delimiter ("tenant-a-") produces a
//     namespace-only prefix that never matches the bare name. Matching is
//     decided on the delimiter boundary, never by string containment, so
//     "tenant-a" does not match "tenant-ab".
//   - TypeWildcard matches segment by segment with '*' globbing; a star never
//     crosses the delimiter.
//
// A pattern is permanently bound to one ResourceKind and can never authorize
// a resource of another kind.
//
// # Decision semantics
//
// Deny entries always win over allow entries for the same principal,
// resource and action; with no matching allow the decision is deny. Actions
// are bit masks and an entry must cover every requested bit.
//
// # Generations and caching
//
// ReplaceACL validates the whole rule set and then commits it atomically
// under a new monotonically increasing generation. The decision cache is
// dropped on every commit (and on consumer-group ownership changes), so a
// cached answer can never extend an old conclusion past its generation.
// In-flight Subscription handles remember their opening generation and
// Authorizer.Reverify re-checks them against the current snapshot after an
// ACL update.
//
// # Consumer groups
//
// Group names are bound to an owning principal. BindGroup and RenameGroup
// reject any attempt to take or reuse a name already owned by another
// principal, closing the cross-tenant rename/reuse path.
package netsec
