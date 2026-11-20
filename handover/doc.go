// Package handover builds and verifies tenant handover credentials for a Go
// virtualization host without accessing hardware.
//
// The host programs device address windows as aligned intervals and page-bit
// permutations (Perm). A Window may contain several Segments that project
// disjoint logical ranges into shared physical memory, so a logically
// contiguous handover request can have a physically scattered image.
//
// Devices can hold in-flight read or write Leases over physical pages. Every
// lease, and every Confirmation that invalidates one, is bound to the exact
// Identity tuple (permanent device id, boot generation, domain generation,
// queue) plus a permission and a physical range. A handover is safe only when
// every lease covering the request's physical image is invalidated by
// confirmations with the same tuple and permission:
//
//   - read leases must be gone so old devices cannot observe the new tenant's
//     data;
//   - write leases must be gone so old devices cannot mutate it;
//   - confirmations may jointly cover a range, but tuples never combine, so
//     generations cannot be stitched (a post-reset same-name queue is a
//     different tuple);
//   - each physical page is decided independently via interval-set
//     arithmetic, never by treating one safe page as representative of a
//     whole range.
//
// All sets are sorted, disjoint, merged IntervalSets over the 48-bit page
// space. No operation allocates an array indexed by the full page-number
// space; permutation images are built from aligned power-of-two cylinders.
//
// Issue returns the maximal safe subset and explicit gaps. Verify recomputes
// the decision from the Manifest (window inventory and certificates) and
// rejects any overstated or stale credential.
package handover
