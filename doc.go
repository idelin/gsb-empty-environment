// Package netsec solves artifact-release witness qualification.
//
// A release is witnessed by signatures produced by certificates held by
// organizations. Organizations are not necessarily independent: a controlling
// party may own several organizations (and each organization may hold several
// certificates). Votes from a single controlling party must never be counted
// more than once, and certificate aliases inside one control domain collapse
// into a single witness.
//
// Solve performs three stages:
//
//   - Control-domain merge: organizations connected by any chain of control
//     edges are unioned into one control domain using a disjoint-set
//     structure, which handles chains, cycles and diamond hierarchies.
//   - Alias normalization: signatures are attributed to their certificate's
//     holder and hence to a control domain; equivalent aliases keep one
//     deterministic atom, and each domain contributes at most one witness.
//   - Subset enumeration: iterative deepening over domain options finds the
//     smallest witness set satisfying the total, region, category and
//     region+category pair thresholds, bounded by the protocol cap of 20.
//
// The result is deterministic: at equal size the lexicographically smallest
// signature identifiers are selected and the returned set is sorted.
package netsec
