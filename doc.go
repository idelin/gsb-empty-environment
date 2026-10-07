// Package pageindex implements an immutable, copy-on-write B-tree index over
// explicitly numbered pages.
//
// Writers replace tree nodes by cloning the modified path onto fresh pages
// and atomically publishing a new root; old roots keep working unchanged.
// Snapshots pin the root of their commit epoch, so long-running analytics
// readers see a stable version while writers keep committing. Snapshot.Close
// is idempotent and safe for concurrent use.
//
// A page becomes reclaimable only when it is unreachable from the current
// root and from every active snapshot (tracked with commit epochs). Recycled
// page numbers are handed out again by the allocator, but never while any
// active reader could still reach them, so reallocation cannot pollute old
// readers. CheckInvariants and Reachable expose a reference-graph checker
// used to verify these guarantees.
package pageindex
