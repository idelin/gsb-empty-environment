// Package netsec is an importable context-monitoring instrumentation library
// for Go hosts that reuse worker threads across requests.
//
// A thread pool that hands a dirty worker to the next request can leak the
// previous request's security identity. This package ingests a stream of
// instrumentation events and keeps, per thread, a strict LIFO stack of
// identity frames:
//
//   - EventSwitch marks a request starting on a worker; switching onto a worker
//     whose stack still belongs to another request raises StaleIdentityRead and
//     preserves the full source chain of the leaked identity.
//   - EventSetIdentity installs the request's base identity frame.
//   - EventBorrow / EventReturn manage nested borrowed scopes. Returns that
//     underflow, pop a base frame, or mismatch the innermost tag raise
//     ReentrancyViolation without corrupting the stack.
//   - EventSpawn models asynchronous derivation. A child may only inherit
//     identity through an explicit Snapshot token; implicit inheritance raises
//     ImplicitInheritance and the child starts clean.
//   - EventExit ends a request and always forces the stack back to zero,
//     raising DirtyExit when borrow scopes were left open.
//
// Every frame carries a Provenance chain (oldest hop first) so a cross-request
// stale read can be traced back through every SetIdentity, Borrow and Snapshot
// hop that propagated the identity.
package netsec
