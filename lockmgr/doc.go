// Package lockmgr implements a multi-granularity two-phase lock manager with
// intention locks, same-transaction lock upgrades, savepoint-aware deadlock
// arbitration and priority queue scheduling.
//
// The resource hierarchy is DB -> Table -> Page -> Row. A transaction must
// acquire the proper intention locks on every ancestor before locking a
// descendant (top-down locking). The five lock modes are:
//
//   - IS  (Intention Shared)
//   - IX  (Intention Exclusive)
//   - S   (Shared)
//   - SIX (Shared + Intention Exclusive)
//   - X   (Exclusive)
//
// Lock requests that cannot be granted immediately are queued per resource.
// Upgrade waiters (the transaction already holds a weaker lock on the same
// resource) are scheduled ahead of ordinary new waiters, and an incompatible
// head waiter blocks every subsequent new request (including compatible
// readers) to prevent writer starvation. Re-entrant requests of an already
// held transaction for a mode it already covers always succeed immediately.
//
// On every blocking event the manager rebuilds a precise waiting-for graph
// (one outgoing edge per currently held incompatible lock), detects every
// minimal deadlock cycle and breaks it through the cheapest valid remedy:
// rolling a victim transaction back to one of its savepoints (releasing only
// the locks acquired after that savepoint and cancelling its pending request)
// or, when no savepoint remedy is valid, aborting the whole transaction.
package lockmgr
