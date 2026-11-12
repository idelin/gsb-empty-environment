// Package netsec implements a privacy-ledger plan state machine for a Go
// privacy host.
//
// Analysts choose the next query from earlier results, so the ledger keeps
// two kinds of plans apart:
//
//   - Fixed plans are predeclared with a fixed number of steps. They cannot
//     branch and complete automatically after their last step.
//   - Adaptive plans fork from a published parent result. A branch may only
//     consume budget reserved by its parent, and a reservation is locked at
//     OpenBranch time.
//
// Every Publish emits an immutable Receipt that binds the parent result
// digest (chained within a plan, or the forked parent receipt for a branch's
// first step) and records the charged cost plus the full fork lineage in
// Path. Publishing the same receipt ID is idempotent so retries never double
// charge.
//
// Budget lifecycle:
//
//   - An unpublished branch in Reserved state can Release, returning its full
//     reservation to the parent.
//   - Once a branch has published, Cancel or Finish forfeits the unspent
//     reservation; nothing is refunded to the parent.
//   - Realized spend (TotalSpent) can never exceed the outstanding
//     reservations, which in turn stay within the ledger's global budget.
package netsec
