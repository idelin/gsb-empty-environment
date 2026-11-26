// Package temporal implements a temporal range algebra and relational
// constraint validation for a relational executor.
//
// Two range domains are supported:
//
//   - Discrete integer ranges (Kind == Discrete). Any endpoint flags are
//     normalized to the canonical closed-open form [lo, hi). Consecutive
//     integer endpoints step by one during normalization, so the open range
//     (3, 4) contains no integer and normalizes to the empty range.
//
//   - Continuous 64-bit microsecond timestamp ranges (Kind == Continuous).
//     Open/closed flags are preserved. (t, t] and [t, t) are empty while
//     [t, t] is the single point t.
//
// Bounds may be unbounded (-inf / +inf). The algebra provides intersection,
// union (with adjacency coalescing), difference and adjacency/overlap
// predicates.
//
// On top of the algebra the package provides:
//
//   - multi-key equality plus range-overlap exclusion constraints
//     (the EXCLUDE USING gist (tenant WITH =, valid_during WITH &&) shape),
//   - foreign-key temporal coverage constraints: every non-empty child
//     interval must be fully covered by the union of adjacent or overlapping
//     parent rows with the same key tuple,
//   - transactional, copy-on-validate-and-commit UPDATE ... FOR PORTION OF
//     style splitting through Table.UpdatePortion. A failed constraint check
//     leaves the table untouched (rollback).
package temporal
