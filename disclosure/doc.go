// Package disclosure implements a pre-publication disclosure review for
// report publishing services.
//
// The protected data is a non-negative integer grid of at most four rows and
// four columns. Every cell carries an inclusive lower and upper bound on its
// possible value. Published row totals, column totals and optional subtotals
// are expressed as linear equality constraints over the cells.
//
// A candidate summary belongs to at most one suppression group: a group is
// either published as a whole or hidden as a whole. Constraints without a
// group are always published.
//
// A sensitive cell is protected only when the published constraints leave at
// least K distinct feasible integer values for it. The value range alone is
// not enough: several subtotal constraints can make the middle of an interval
// infeasible, so the review enumerates the full set of feasible values and
// records every gap with a re-checkable exclusion reason. For every value
// claimed feasible, a complete witness table satisfying every published
// constraint is returned.
//
// Review searches every combination of hidden groups for the minimum total
// hiding cost that protects every sensitive cell. Ties are broken by the
// lexicographic ordering of the sorted group ID sequences. A solution whose
// cost exceeds the configured budget is never approved: when the budget is
// exhausted the decision is rejected with a re-checkable reason.
//
// All indices in the API are zero-based.
package disclosure
