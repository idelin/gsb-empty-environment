// Package txn implements a systemd-style service-unit dependency transaction
// solver and execution scheduler.
//
// The solver reproduces the three counter-intuitive core semantics of
// systemd's transaction engine:
//
//   - Requirement dependencies (Requires/Requisite/Wants/BindsTo/PartOf/
//     Conflicts) are fully orthogonal to ordering (Before/After). A
//     requirement only pulls jobs into the transaction; it never creates an
//     ordering edge. Ordering is derived exclusively from Before/After.
//   - Requisite never pulls a start job. Its target must already be active in
//     the supplied state snapshot, otherwise the whole transaction fails.
//   - Start/stop collisions on one unit are arbitrated by transaction mode:
//     fail rolls back the whole transaction; replace only prunes a side that
//     is non-anchor and eliminable; replace-irreversibly additionally allows
//     replacing non-anchor irreducible jobs. Anchors are never replaced.
//
// Ordering edges are polarised: start-like jobs follow the declared
// Before/After direction, while any edge touching a stop job is reversed, so
// shutdown runs in the opposite order of startup. Remaining ordering cycles
// are broken deterministically: the shortest cycle of each strongly connected
// component is located, a non-critical edge (an edge touching an anchor job is
// critical) is deleted, and every cut cycle/edge is reported. Jobs themselves
// are never dropped to break a cycle.
package txn
