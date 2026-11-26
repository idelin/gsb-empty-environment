// Package mvrewrite decides set-semantic rewrites of acyclic inner/left-outer
// join queries from a single precomputed materialized view.
//
// The primary entry point is Analyze.  It returns one Decision per view:
// rewritable views carry a serializable DNF compensation filter and dedup
// projection; non-rewritable views carry a minimized at-most-two-rows-per-table
// semantic counterexample and, for null-discrimination failures, a two-instance
// padding-versus-real-NULL ambiguity proof.
package mvrewrite
