// Package semijoin computes minimum worst-case adaptive read costs for
// maintaining grouped SQL semi-join results under count summaries.
//
// Each Variable describes one state (Initial or Final) of one shard-level
// deduplicated key count or NULL count. A width greater than zero is an
// uncertain count and may span at most five values (0..4). Larger intervals
// are accepted only when their sign is fixed; such counts are represented
// abstractly and are not enumerated. Linear constraints are shared across
// neighboring summaries and are checked during feasibility enumeration.
//
// OuterRow carries a group, shard, nullable outer key, endpoint
// multiplicities, and a signed batch delta. Endpoint multiplicities must be
// non-negative; intermediate batch steps may cancel and are therefore not
// required to stay non-negative. The predicate follows SQL three-valued
// logic: NULL comparisons are UNKNOWN, EXISTS is true only on a true match,
// NOT EXISTS is false on UNKNOWN, and NOT IN becomes false when a NULL in
// the same shard can make its subquery UNKNOWN.
//
// Pages reveal exact counts. Solve enumerates feasible small worlds per
// conflict component, joins components linked by a shared page, and builds
// an exact adaptive decision tree minimizing worst-case read cost. If no
// readable tree can distinguish two feasible databases with different
// deltas, Solve returns the smallest differing witness pair instead of a
// policy.
package semijoin
