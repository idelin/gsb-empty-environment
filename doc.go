// Package netsec computes the semantic diff between two versions of a
// cloud access policy, for use by a Go review host.
//
// Subjects, actions, resources and tags are modelled as finite
// domains (Domain). A Policy is a list of rules; each rule carries a
// boolean condition (a Rect: a per-dimension set of accepted values,
// missing dimensions are wildcards) and an effect (Allow or Deny).
// Deny takes precedence: a point is allowed iff some Allow rule
// matches it and no Deny rule does.
//
// Diff computes the set of points that are newly allowed and were
// previously rejected, and returns it as a non-overlapping rectangle
// cover, each rectangle annotated with the rules that hit it. A
// shard budget bounds the number of completed rectangles; when the
// budget is exceeded, Diff returns the not-yet-covered remainder
// domain so the caller can re-shard it (see Clip). Verify
// exhaustively checks a Result against both policies on a shrunk
// domain, proving no misses and no overlaps.
package netsec
